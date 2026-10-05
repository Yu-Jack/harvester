package componenthealth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

type recordingComponentHealthController struct {
	ctlharvesterv1.ComponentHealthController
	enqueued []string
}

func (controller *recordingComponentHealthController) Enqueue(name string) {
	controller.enqueued = append(controller.enqueued, name)
}

func TestVMBackupCallbacksEnqueueComponentHealth(t *testing.T) {
	controller := &recordingComponentHealthController{}
	handler := &Handler{componentHealthController: controller}
	for index := 0; index < 100; index++ {
		backup := newVMBackup(fmt.Sprintf("backup-%d", index), nil)
		result, err := handler.OnVMBackupChanged("default/"+backup.Name, backup)
		assert.Same(t, backup, result)
		assert.NoError(t, err)
	}
	assert.Len(t, controller.enqueued, 100)
	for _, name := range controller.enqueued {
		assert.Equal(t, vmBackupComponentHealthName, name)
	}
}

func TestEnqueueInitialHealth(t *testing.T) {
	controller := &recordingComponentHealthController{}
	handler := &Handler{
		componentHealthController: controller,
		healthCachesSynced:        []cache.InformerSynced{func() bool { return true }},
	}
	handler.enqueueInitialHealth(context.Background())
	assert.Equal(t, []string{
		nodeComponentHealthName,
		vmComponentHealthName,
		volumeComponentHealthName,
		vmBackupComponentHealthName,
		scheduleVMBackupComponentHealthName,
	}, controller.enqueued)
}

func TestEnqueueInitialHealthWaitsForCachesAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := &recordingComponentHealthController{}
	checked := make(chan struct{}, 1)
	handler := &Handler{
		componentHealthController: controller,
		healthCachesSynced: []cache.InformerSynced{func() bool {
			select {
			case checked <- struct{}{}:
			default:
			}
			return false
		}},
	}
	done := make(chan struct{})
	go func() {
		handler.enqueueInitialHealth(ctx)
		close(done)
	}()
	select {
	case <-checked:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not wait for cache synchronization")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not stop on cancellation")
	}
	assert.Empty(t, controller.enqueued)
}

func TestOnComponentHealthChangedDispatch(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: nodeComponentHealthName},
		{name: vmComponentHealthName},
		{name: volumeComponentHealthName},
		{name: vmBackupComponentHealthName},
		{name: scheduleVMBackupComponentHealthName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			handler := &Handler{
				componentHealths:      fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				nodeCache:             fakeclients.NodeCache(clientset.CoreV1().Nodes),
				vmiCache:              fakeclients.VirtualMachineInstanceCache(clientset.KubevirtV1().VirtualMachineInstances),
				volumeCache:           fakeclients.LonghornVolumeCache(clientset.LonghornV1beta2().Volumes),
				vmBackupCache:         fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups),
				scheduleVMBackupCache: fakeclients.SVMBackupCache(clientset.HarvesterhciV1beta1().ScheduleVMBackups),
				healthCachesSynced:    []cache.InformerSynced{func() bool { return true }},
			}
			result, err := handler.OnComponentHealthChanged(tc.name, nil)
			assert.Nil(t, result)
			if !assert.NoError(t, err) {
				return
			}
			health, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), tc.name, metav1.GetOptions{})
			if !assert.NoError(t, err) {
				return
			}
			assert.Empty(t, health.Status.Checks)
			result, err = handler.OnComponentHealthChanged(tc.name, health)
			assert.Same(t, health, result)
			assert.NoError(t, err)
			for _, action := range clientset.Actions() {
				assert.False(t, action.Matches("update", "componenthealths"))
			}
		})
	}
}

func TestOnComponentHealthChangedWaitsForCaches(t *testing.T) {
	handler := &Handler{
		healthCachesSynced: []cache.InformerSynced{
			func() bool { return true },
			func() bool { return false },
		},
	}
	result, err := handler.OnComponentHealthChanged(vmBackupComponentHealthName, nil)
	assert.Nil(t, result)
	assert.EqualError(t, err, "ComponentHealth resource caches are not synced")
}

func TestOnComponentHealthChangedIgnoresUnownedKeys(t *testing.T) {
	handler := &Handler{}
	health := &v1beta1.ComponentHealth{ObjectMeta: metav1.ObjectMeta{Name: "other-component"}}
	result, err := handler.OnComponentHealthChanged(health.Name, health)
	assert.Same(t, health, result)
	assert.NoError(t, err)
	result, err = handler.OnComponentHealthChanged(health.Name, nil)
	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestOnComponentHealthChangedReturnsReconcileErrors(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("list", "virtualmachinebackups", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("temporary cache failure")
	})
	handler := &Handler{
		vmBackupCache: fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups),
	}
	result, err := handler.OnComponentHealthChanged(vmBackupComponentHealthName, nil)
	assert.Nil(t, result)
	assert.EqualError(t, err, "failed to list VirtualMachineBackups: temporary cache failure")
}

func TestOnComponentHealthChangedStopsUpdatingAfterChecksClear(t *testing.T) {
	health := &v1beta1.ComponentHealth{
		ObjectMeta: metav1.ObjectMeta{Name: vmBackupComponentHealthName},
		Status: v1beta1.ComponentHealthStatus{
			Checks: map[string]v1beta1.CheckResult{
				checkKeyVMBackupFailed: {Severity: v1beta1.SeverityError, Message: "stale backup failure"},
			},
		},
	}
	clientset := fake.NewSimpleClientset(health)
	handler := &Handler{
		componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		vmBackupCache:    fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups),
	}
	_, err := handler.OnComponentHealthChanged(health.Name, health)
	if !assert.NoError(t, err) {
		return
	}
	updated, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), health.Name, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	assert.Empty(t, updated.Status.Checks)
	_, err = handler.OnComponentHealthChanged(updated.Name, updated)
	assert.NoError(t, err)
	updateCalls := 0
	for _, action := range clientset.Actions() {
		if action.Matches("update", "componenthealths") {
			updateCalls++
		}
	}
	assert.Equal(t, 1, updateCalls)
}
