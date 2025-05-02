package data

import (
	"bytes"
	"context"
	"fmt"

	"github.com/rancher/wrangler/v3/pkg/apply"
	"github.com/rancher/wrangler/v3/pkg/yaml"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/harvester/harvester/pkg/config"
)

// Init adds built-in resources
func Init(ctx context.Context, mgmtCtx *config.Management, options config.Options) error {
	if err := createCRDs(ctx, mgmtCtx.RestConfig); err != nil {
		return err
	}
	if err := Create(ctx, mgmtCtx.RestConfig); err != nil {
		return err
	}

	if err := addPublicNamespace(mgmtCtx.Apply); err != nil {
		return err
	}
	if err := addFleetDefaultNamespace(mgmtCtx.Apply); err != nil {
		return err
	}
	if err := addAPIService(mgmtCtx.Apply, options.Namespace); err != nil {
		return err
	}
	if err := addAuthenticatedRoles(mgmtCtx.Apply); err != nil {
		return err
	}

	// Not applying the built-in templates and secrets in case users have edited them.
	if err := createTemplates(mgmtCtx, publicNamespace); err != nil {
		return err
	}
	return createSecrets(mgmtCtx)
}

func Create(ctx context.Context, cfg *rest.Config) error {
	applyClient, err := apply.NewForConfig(cfg)
	if err != nil {
		return err
	}
	objs, err := generateObjects()
	if err != nil {
		return fmt.Errorf("error generating objects: %v", err)
	}

	return applyClient.WithDynamicLookup().WithContext(ctx).WithSetID("seeder-crd").ApplyObjects(objs...)
}

func generateObjects() ([]runtime.Object, error) {
	var objs []runtime.Object
	for _, v := range AssetNames() {
		content, err := Asset(v)
		if err != nil {
			return nil, err
		}
		obj, err := yaml.ToObjects(bytes.NewReader(content))
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj...)
	}

	return objs, nil
}
