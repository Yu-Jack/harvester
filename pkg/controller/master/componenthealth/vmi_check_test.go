package componenthealth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func TestReconcileVMIOnlyUsesVMICache(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	handler := &Handler{
		componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		vmiCache:         fakeclients.VirtualMachineInstanceCache(clientset.KubevirtV1().VirtualMachineInstances),
	}

	err := handler.reconcileVMI()
	assert.NoError(t, err)

	componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), vmComponentHealthName, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, componentName, componentHealth.Labels[v1beta1.LabelKeyComponent])
}
