terraform {
  required_version = "= 1.5.7"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "7.29.0"
    }
  }

  backend "gcs" {
    bucket = "libops-triplet-terraform"
    prefix = "/github"
  }
}

provider "google" {
  project = var.project
}

locals {
  bucket_name       = var.data_bucket_name != "" ? var.data_bucket_name : "${var.project}-data"
  cache_bucket_name = var.cache_bucket_name != "" ? var.cache_bucket_name : "${var.project}-triplet-cache"
  enable_https      = length(var.domain_names) > 0
  enable_redirect   = var.enable_http_redirect && local.enable_https
}

resource "google_storage_bucket" "data" {
  project                     = var.project
  name                        = local.bucket_name
  location                    = "US"
  uniform_bucket_level_access = true
}

data "google_service_account" "cr" {
  project    = var.project
  account_id = var.service_account_id
}

resource "google_storage_bucket_iam_member" "cr_object_viewer" {
  bucket = google_storage_bucket.data.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${data.google_service_account.cr.email}"
}

resource "google_storage_bucket" "cache" {
  project                     = var.project
  name                        = local.cache_bucket_name
  location                    = "US"
  uniform_bucket_level_access = true
}

resource "google_storage_bucket_iam_member" "cr_cache_object_admin" {
  bucket = google_storage_bucket.cache.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${data.google_service_account.cr.email}"
}

resource "google_secret_manager_secret" "triplet_config" {
  project   = var.project
  secret_id = "${var.service_name}-config"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "triplet_config" {
  secret      = google_secret_manager_secret.triplet_config.id
  secret_data = var.config_yaml
}

resource "google_secret_manager_secret_iam_member" "cr_config_accessor" {
  project   = var.project
  secret_id = google_secret_manager_secret.triplet_config.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${data.google_service_account.cr.email}"
}

resource "google_cloud_run_v2_service" "triplet" {
  for_each = toset(var.regions)

  project             = var.project
  name                = var.service_name
  location            = each.value
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account = data.google_service_account.cr.email

    containers {
      image = var.image
      args  = ["-config", "/etc/triplet/config.yaml"]

      ports {
        container_port = 8080
      }

      volume_mounts {
        name       = "triplet-config"
        mount_path = "/etc/triplet"
      }
    }

    volumes {
      name = "triplet-config"

      secret {
        secret = google_secret_manager_secret.triplet_config.secret_id

        items {
          version = "latest"
          path    = "config.yaml"
        }
      }
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.cr_config_accessor,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "public_invoker" {
  for_each = var.allow_unauthenticated ? toset(var.regions) : toset([])

  project  = var.project
  location = each.value
  name     = google_cloud_run_v2_service.triplet[each.value].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_compute_global_address" "triplet" {
  project = var.project
  name    = "${var.service_name}-lb-ip"
}

resource "google_compute_region_network_endpoint_group" "triplet" {
  for_each = toset(var.regions)

  project               = var.project
  name                  = "${var.service_name}-${each.value}-neg"
  network_endpoint_type = "SERVERLESS"
  region                = each.value

  cloud_run {
    service = google_cloud_run_v2_service.triplet[each.value].name
  }

  depends_on = [google_cloud_run_v2_service.triplet]
}

resource "google_compute_backend_service" "triplet" {
  project               = var.project
  name                  = "${var.service_name}-backend"
  protocol              = "HTTP"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  timeout_sec           = 300

  dynamic "backend" {
    for_each = google_compute_region_network_endpoint_group.triplet
    content {
      group = backend.value.id
    }
  }

  log_config {
    enable      = true
    sample_rate = 1
  }
}

resource "google_compute_url_map" "triplet" {
  project         = var.project
  name            = "${var.service_name}-url-map"
  default_service = google_compute_backend_service.triplet.id
}

resource "google_compute_managed_ssl_certificate" "triplet" {
  count   = length(var.domain_names) > 0 ? 1 : 0
  project = var.project
  name    = "${var.service_name}-cert"

  managed {
    domains = var.domain_names
  }
}

resource "google_compute_target_https_proxy" "triplet" {
  count   = local.enable_https ? 1 : 0
  project = var.project
  name    = "${var.service_name}-https-proxy"
  url_map = google_compute_url_map.triplet.id

  ssl_certificates = [
    google_compute_managed_ssl_certificate.triplet[0].id
  ]
}

resource "google_compute_global_forwarding_rule" "triplet_https" {
  count                 = local.enable_https ? 1 : 0
  project               = var.project
  name                  = "${var.service_name}-https"
  ip_address            = google_compute_global_address.triplet.id
  port_range            = "443"
  target                = google_compute_target_https_proxy.triplet[0].id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

resource "google_compute_url_map" "triplet_http_redirect" {
  count   = local.enable_redirect ? 1 : 0
  project = var.project
  name    = "${var.service_name}-http-redirect"

  default_url_redirect {
    https_redirect         = true
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
    strip_query            = false
  }
}

resource "google_compute_target_http_proxy" "triplet_redirect" {
  count   = local.enable_redirect ? 1 : 0
  project = var.project
  name    = "${var.service_name}-http-proxy"
  url_map = google_compute_url_map.triplet_http_redirect[0].id
}

resource "google_compute_global_forwarding_rule" "triplet_http" {
  count                 = local.enable_redirect ? 1 : 0
  project               = var.project
  name                  = "${var.service_name}-http"
  ip_address            = google_compute_global_address.triplet.id
  port_range            = "80"
  target                = google_compute_target_http_proxy.triplet_redirect[0].id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

output "load_balancer_ip" {
  description = "Global load balancer IP address."
  value       = google_compute_global_address.triplet.address
}
