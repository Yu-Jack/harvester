package fakeclients

import (
	"context"

	"github.com/rancher/wrangler/v3/pkg/generic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	harvesterv1type "github.com/harvester/harvester/pkg/generated/clientset/versioned/typed/harvesterhci.io/v1beta1"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
)

type ComponentHealthClient func() harvesterv1type.ComponentHealthInterface

func (c ComponentHealthClient) Create(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c().Create(context.TODO(), componentHealth, metav1.CreateOptions{})
}

func (c ComponentHealthClient) Update(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c().Update(context.TODO(), componentHealth, metav1.UpdateOptions{})
}

func (c ComponentHealthClient) UpdateStatus(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c().UpdateStatus(context.TODO(), componentHealth, metav1.UpdateOptions{})
}

func (c ComponentHealthClient) Delete(name string, options *metav1.DeleteOptions) error {
	return c().Delete(context.TODO(), name, *options)
}

func (c ComponentHealthClient) Get(name string, options metav1.GetOptions) (*harvesterv1.ComponentHealth, error) {
	return c().Get(context.TODO(), name, options)
}

func (c ComponentHealthClient) List(options metav1.ListOptions) (*harvesterv1.ComponentHealthList, error) {
	return c().List(context.TODO(), options)
}

func (c ComponentHealthClient) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return c().Watch(context.TODO(), options)
}

func (c ComponentHealthClient) Patch(name string, patchType types.PatchType, data []byte, subresources ...string) (*harvesterv1.ComponentHealth, error) {
	return c().Patch(context.TODO(), name, patchType, data, metav1.PatchOptions{}, subresources...)
}

func (c ComponentHealthClient) WithImpersonation(_ rest.ImpersonationConfig) (generic.NonNamespacedClientInterface[*harvesterv1.ComponentHealth, *harvesterv1.ComponentHealthList], error) {
	panic("implement me")
}

type ComponentHealthCache func() harvesterv1type.ComponentHealthInterface

func (c ComponentHealthCache) Get(name string) (*harvesterv1.ComponentHealth, error) {
	return c().Get(context.TODO(), name, metav1.GetOptions{})
}

func (c ComponentHealthCache) List(selector labels.Selector) ([]*harvesterv1.ComponentHealth, error) {
	list, err := c().List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	result := make([]*harvesterv1.ComponentHealth, 0, len(list.Items))
	for index := range list.Items {
		result = append(result, &list.Items[index])
	}
	return result, nil
}

func (c ComponentHealthCache) AddIndexer(_ string, _ generic.Indexer[*harvesterv1.ComponentHealth]) {
	panic("implement me")
}

func (c ComponentHealthCache) GetByIndex(_, _ string) ([]*harvesterv1.ComponentHealth, error) {
	panic("implement me")
}

type ComponentHealthController struct {
	ctlharvesterv1.ComponentHealthController
	ComponentHealthClient
}

func (c ComponentHealthController) Create(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c.ComponentHealthClient.Create(componentHealth)
}

func (c ComponentHealthController) Update(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c.ComponentHealthClient.Update(componentHealth)
}

func (c ComponentHealthController) UpdateStatus(componentHealth *harvesterv1.ComponentHealth) (*harvesterv1.ComponentHealth, error) {
	return c.ComponentHealthClient.UpdateStatus(componentHealth)
}

func (c ComponentHealthController) Delete(name string, options *metav1.DeleteOptions) error {
	return c.ComponentHealthClient.Delete(name, options)
}

func (c ComponentHealthController) Get(name string, options metav1.GetOptions) (*harvesterv1.ComponentHealth, error) {
	return c.ComponentHealthClient.Get(name, options)
}

func (c ComponentHealthController) List(options metav1.ListOptions) (*harvesterv1.ComponentHealthList, error) {
	return c.ComponentHealthClient.List(options)
}

func (c ComponentHealthController) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return c.ComponentHealthClient.Watch(options)
}

func (c ComponentHealthController) Patch(name string, patchType types.PatchType, data []byte, subresources ...string) (*harvesterv1.ComponentHealth, error) {
	return c.ComponentHealthClient.Patch(name, patchType, data, subresources...)
}

func (c ComponentHealthController) WithImpersonation(impersonate rest.ImpersonationConfig) (generic.NonNamespacedClientInterface[*harvesterv1.ComponentHealth, *harvesterv1.ComponentHealthList], error) {
	return c.ComponentHealthClient.WithImpersonation(impersonate)
}
