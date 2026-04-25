# Cloud Run deploy

Multi-region Cloud Run deployment of triplet, mirroring the
[`cantaloupe-cloudrun`](https://github.com/libops/cantaloupe-cloudrun) layout.

## Prerequisites

- A GCP project with Artifact Registry, Cloud Run, and Cloud Storage APIs enabled.
- A pushed `triplet` container image (e.g. `gcr.io/<project>/triplet:<tag>`).
- A pre-existing service account named `cr-triplet`. Terraform grants it
  `roles/storage.objectViewer` on the source bucket and `roles/storage.objectAdmin`
  on the derivative/source cache bucket.
- Terraform 1.5.x and the GCS state bucket from the libops infra setup.

## Configuration

Edit `terraform.tfvars`:

```hcl
project = "my-gcp-project"
image   = "gcr.io/my-gcp-project/triplet:v0.1.0"
regions = [
  "us-east4",
  "us-central1",
  "us-west1",
]
```

The triplet config YAML is rendered into a Cloud Run secret (or mounted
config map) and passed via `-config /etc/triplet/config.yaml` to the entrypoint.

The Terraform in this directory provisions:

- the source data bucket
- a cache bucket for derivative/source caching
- IAM grants for the `cr-triplet` service account
- one Cloud Run service per configured region

## Apply

```sh
terraform init
terraform apply
```
