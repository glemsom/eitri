package provider

import "net/http"

func resolveClient(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}
