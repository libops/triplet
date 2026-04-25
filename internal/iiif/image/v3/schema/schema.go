package schema

import iifschema "github.com/libops/iiif-spec/image/v3/schema"

// ValidateInfoBytes validates an Image API info.json payload against the
// iiif-spec maintained derived schema.
func ValidateInfoBytes(doc []byte) error {
	return iifschema.ValidateInfoBytes(doc)
}
