terraform {
  required_version = "= 1.5.7"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "7.10.0"
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
  bucket_name = var.data_bucket_name != "" ? var.data_bucket_name : "${var.project}-data"
  cache_bucket_name = var.cache_bucket_name != "" ? var.cache_bucket_name : "${var.project}-triplet-cache"
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

# One Cloud Run service per region. A separate global LB module fronts them
# (see ../cloudrun/modules/lb in cantaloupe-cloudrun for the load-balancing
# pattern; the triplet equivalent will be added once milestone 1 ships
# end-to-end traffic).
module "triplet" {
  for_each = toset(var.regions)
  source   = "git::https://github.com/libops/terraform-cloudrun-v2?ref=0.3.3"

  name            = var.service_name
  project         = var.project
  region          = each.value
  image           = var.image
  service_account = data.google_service_account.cr.email

  # triplet listens on 8080 and reads its config from a mounted secret/file.
  # The actual mounting is left to the operator until the LB module lands.
  port = 8080
}
