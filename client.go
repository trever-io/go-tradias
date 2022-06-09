package tradias

type APIClient struct {
	apiKey    string
	apiSecret string
}

func NewClient(apiKey, apiSecret string) *APIClient {
	return &APIClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
	}
}
