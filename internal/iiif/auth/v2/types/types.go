package types

const (
	ContextAuth2 = "http://iiif.io/api/auth/2/context.json"

	TypeProbeResult        = "AuthProbeResult2"
	TypeProbeService       = "AuthProbeService2"
	TypeAccessService      = "AuthAccessService2"
	TypeAccessTokenService = "AuthAccessTokenService2"
	TypeLogoutService      = "AuthLogoutService2"
)

type ProbeResult struct {
	Context string `json:"@context"`
	Type    string `json:"type"`
	Status  int    `json:"status"`
}

type TokenResult struct {
	Context     string `json:"@context,omitempty"`
	AccessToken string `json:"accessToken,omitempty"`
	ExpiresIn   int    `json:"expiresIn,omitempty"`
	MessageId   string `json:"messageId,omitempty"`
	Error       string `json:"error,omitempty"`
}
