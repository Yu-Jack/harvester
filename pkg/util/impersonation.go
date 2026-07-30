package util

import (
	"fmt"

	"k8s.io/client-go/rest"
)

// WithUserImpersonation builds an impersonated REST config and calls fn with it.
func WithUserImpersonation(restConfig *rest.Config, username string, groups []string, fn func(*rest.Config) error) error {
	if username == "" {
		return fmt.Errorf("missing username for impersonation")
	}
	userConfig := rest.CopyConfig(restConfig)
	userConfig.Impersonate = rest.ImpersonationConfig{
		UserName: username,
		Groups:   groups,
	}
	return fn(userConfig)
}
