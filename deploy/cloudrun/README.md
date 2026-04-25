# Cloud Run deploy

Multi-region Cloud Run deployment of triplet, mirroring the
[`cantaloupe-cloudrun`](https://github.com/libops/cantaloupe-cloudrun) layout.

## Prerequisites

- A GCP project with Artifact Registry, Cloud Run, Cloud Storage, and Secret
  Manager APIs enabled.
- A pushed `triplet` container image (e.g. `gcr.io/<project>/triplet:<tag>`).
- A pre-existing service account named `cr-triplet`. Terraform grants it
  `roles/storage.objectViewer` on the source bucket and `roles/storage.objectAdmin`
  on the derivative/source cache bucket, plus Secret Manager access to the
  mounted triplet config.
- Terraform 1.5.x and the GCS state bucket from the libops infra setup.

## Configuration

Edit `terraform.tfvars`:

```hcl
project = "my-gcp-project"
image   = "gcr.io/my-gcp-project/triplet:v0.1.0"
config_yaml = file("config.cloudrun.yaml")
regions = [
  "us-east4",
  "us-central1",
  "us-west1",
]
domain_names = ["iiif.example.org"]
```

Terraform stores `config_yaml` in Secret Manager and mounts it into each Cloud
Run revision at `/etc/triplet/config.yaml`. The container is invoked with
`-config /etc/triplet/config.yaml`; no manual config mount is required.

The Terraform in this directory provisions:

- the source data bucket
- a cache bucket for derivative/source caching
- IAM grants for the `cr-triplet` service account
- one Cloud Run service per configured region
- serverless NEGs, global HTTPS load balancer, optional managed certificate,
  and optional HTTP-to-HTTPS redirect

## Apply

```sh
terraform init
terraform apply
```
