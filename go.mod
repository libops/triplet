module github.com/libops/triplet

go 1.26.2

require (
	github.com/davidbyttow/govips/v2 v2.19.0
	github.com/go-sql-driver/mysql v1.10.1
	github.com/libops/iiif-spec v0.3.0
	github.com/prometheus/client_golang v1.25.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.72.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/davidbyttow/govips/v2 => github.com/libops/govips/v2 v2.0.0-20260429094915-ee9c36250135
