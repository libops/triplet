variable "project" {
  description = "GCP project ID."
  type        = string
}

variable "image" {
  description = "Fully-qualified container image URL for triplet."
  type        = string
}

variable "config_yaml" {
  description = "Complete triplet YAML config mounted into Cloud Run at /etc/triplet/config.yaml."
  type        = string
  sensitive   = true
}

variable "regions" {
  description = "Cloud Run regions to deploy to. The first region is treated as primary."
  type        = list(string)
  default = [
    "us-east4",
    "us-central1",
    "us-west1",
  ]
}

variable "service_name" {
  description = "Cloud Run service name."
  type        = string
  default     = "triplet"
}

variable "service_account_id" {
  description = "Pre-existing service account used by the Cloud Run revisions."
  type        = string
  default     = "cr-triplet"
}

variable "data_bucket_name" {
  description = "Override the source bucket name. Defaults to <project>-data."
  type        = string
  default     = ""
}

variable "cache_bucket_name" {
  description = "Override the derivative/source cache bucket name. Defaults to <project>-triplet-cache."
  type        = string
  default     = ""
}

variable "domain_names" {
  description = "Optional hostnames for the global HTTPS load balancer managed certificate."
  type        = list(string)
  default     = []
}

variable "enable_http_redirect" {
  description = "Create an HTTP forwarding rule that redirects to HTTPS."
  type        = bool
  default     = true
}

variable "allow_unauthenticated" {
  description = "Grant allUsers roles/run.invoker on each Cloud Run service. Required for public IIIF endpoints and the external load balancer."
  type        = bool
  default     = true
}
