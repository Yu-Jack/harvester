package pod

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util"
	"github.com/harvester/harvester/pkg/util/fakeclients"
	"github.com/harvester/harvester/pkg/webhook/types"
)

func Test_envPatches(t *testing.T) {

	type input struct {
		targetEnvs []corev1.EnvVar
		proxyEnvs  []corev1.EnvVar
		basePath   string
	}
	var testCases = []struct {
		name   string
		input  input
		output types.PatchOps
	}{
		{
			name: "add proxy envs",
			input: input{
				targetEnvs: []corev1.EnvVar{
					{
						Name:  "foo",
						Value: "bar",
					},
				},
				proxyEnvs: []corev1.EnvVar{
					{
						Name:  util.HTTPProxyEnv,
						Value: "http://192.168.0.1:3128",
					},
					{
						Name:  util.HTTPSProxyEnv,
						Value: "http://192.168.0.1:3128",
					},
					{
						Name:  util.NoProxyEnv,
						Value: "127.0.0.1,0.0.0.0,10.0.0.0/8",
					},
				},
				basePath: "/spec/containers/0/env",
			},
			output: []string{
				`{"op": "add", "path": "/spec/containers/0/env/-", "value": {"name":"HTTP_PROXY","value":"http://192.168.0.1:3128"}}`,
				`{"op": "add", "path": "/spec/containers/0/env/-", "value": {"name":"HTTPS_PROXY","value":"http://192.168.0.1:3128"}}`,
				`{"op": "add", "path": "/spec/containers/0/env/-", "value": {"name":"NO_PROXY","value":"127.0.0.1,0.0.0.0,10.0.0.0/8"}}`,
			},
		},
		{
			name: "add proxy envs to empty envs",
			input: input{
				targetEnvs: []corev1.EnvVar{},
				proxyEnvs: []corev1.EnvVar{
					{
						Name:  util.HTTPProxyEnv,
						Value: "http://192.168.0.1:3128",
					},
					{
						Name:  util.HTTPSProxyEnv,
						Value: "http://192.168.0.1:3128",
					},
					{
						Name:  util.NoProxyEnv,
						Value: "127.0.0.1,0.0.0.0,10.0.0.0/8",
					},
				},
				basePath: "/spec/containers/0/env",
			},
			output: []string{
				`{"op": "add", "path": "/spec/containers/0/env", "value": [{"name":"HTTP_PROXY","value":"http://192.168.0.1:3128"}]}`,
				`{"op": "add", "path": "/spec/containers/0/env/-", "value": {"name":"HTTPS_PROXY","value":"http://192.168.0.1:3128"}}`,
				`{"op": "add", "path": "/spec/containers/0/env/-", "value": {"name":"NO_PROXY","value":"127.0.0.1,0.0.0.0,10.0.0.0/8"}}`,
			},
		},
	}
	for _, testCase := range testCases {
		result, err := envPatches(testCase.input.targetEnvs, testCase.input.proxyEnvs, testCase.input.basePath)
		assert.Equal(t, testCase.output, result)
		assert.Empty(t, err)
	}
}

func Test_volumePatch(t *testing.T) {

	type input struct {
		target []corev1.Volume
		volume corev1.Volume
	}
	var testCases = []struct {
		name   string
		input  input
		output string
	}{
		{
			name: "add additional ca volume",
			input: input{
				target: []corev1.Volume{
					{
						Name: "foo",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				volume: corev1.Volume{
					Name: "additional-ca-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							DefaultMode: pointer.Int32(400),
							SecretName:  util.AdditionalCASecretName,
						},
					},
				},
			},
			output: `{"op": "add", "path": "/spec/volumes/-", "value": {"name":"additional-ca-volume","secret":{"secretName":"harvester-additional-ca","defaultMode":400}}}`,
		},
		{
			name: "add additional ca volume to empty volumes",
			input: input{
				target: []corev1.Volume{},
				volume: corev1.Volume{
					Name: "additional-ca-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							DefaultMode: pointer.Int32(400),
							SecretName:  util.AdditionalCASecretName,
						},
					},
				},
			},
			output: `{"op": "add", "path": "/spec/volumes", "value": [{"name":"additional-ca-volume","secret":{"secretName":"harvester-additional-ca","defaultMode":400}}]}`,
		},
	}
	for _, testCase := range testCases {
		result, err := volumePatch(testCase.input.target, testCase.input.volume)
		assert.Equal(t, testCase.output, result)
		assert.Empty(t, err)
	}
}

func Test_volumeMountPatch(t *testing.T) {

	type input struct {
		target      []corev1.VolumeMount
		volumeMount corev1.VolumeMount
		path        string
	}
	var testCases = []struct {
		name   string
		input  input
		output string
	}{
		{
			name: "add additional ca volume mount",
			input: input{
				target: []corev1.VolumeMount{
					{
						Name:      "foo",
						MountPath: "/bar",
					},
				},
				volumeMount: corev1.VolumeMount{
					Name:      "additional-ca-volume",
					MountPath: "/etc/ssl/certs/" + util.AdditionalCAFileName,
					SubPath:   util.AdditionalCAFileName,
					ReadOnly:  true,
				},
				path: "/spec/containers/0/volumeMounts",
			},
			output: `{"op": "add", "path": "/spec/containers/0/volumeMounts/-", "value": {"name":"additional-ca-volume","readOnly":true,"mountPath":"/etc/ssl/certs/additional-ca.pem","subPath":"additional-ca.pem"}}`,
		},
		{
			name: "add additional ca volume mount to empty volumeMounts",
			input: input{
				target: []corev1.VolumeMount{},
				volumeMount: corev1.VolumeMount{
					Name:      "additional-ca-volume",
					MountPath: "/etc/ssl/certs/" + util.AdditionalCAFileName,
					SubPath:   util.AdditionalCAFileName,
					ReadOnly:  true,
				},
				path: "/spec/containers/0/volumeMounts",
			},
			output: `{"op": "add", "path": "/spec/containers/0/volumeMounts", "value": [{"name":"additional-ca-volume","readOnly":true,"mountPath":"/etc/ssl/certs/additional-ca.pem","subPath":"additional-ca.pem"}]}`,
		},
	}
	for _, testCase := range testCases {
		result, err := volumeMountPatch(testCase.input.target, testCase.input.path, testCase.input.volumeMount)
		assert.Equal(t, testCase.output, result)
		assert.Empty(t, err)
	}
}

func TestPatchMigrationCPUNodeSelector(t *testing.T) {
	newMigrationPod := func(nodeSelector map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "virt-launcher-test",
				Namespace: "default",
				Labels: map[string]string{
					kubevirtv1.MigrationJobLabel:       "migration-uid",
					kubevirtv1.VirtualMachineNameLabel: "vm1",
				},
				Annotations: map[string]string{
					kubevirtv1.DomainAnnotation: "vm1",
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: nodeSelector,
			},
		}
	}

	migrationCPUSelector := map[string]string{
		kubevirtv1.CPUFeatureLabel + "aes":                   "true",
		kubevirtv1.CPUModelLabel + "Skylake-Client":          "true",
		kubevirtv1.SupportedHostModelMigrationCPU + "x86_64": "true",
		kubevirtv1.CPUModelVendorLabel + "Intel":             "true",
		kubevirtv1.HostModelCPULabel + "Skylake-Client":      "true",
		kubevirtv1.HostModelRequiredFeaturesLabel + "ssse3":  "true",
		kubevirtv1.CPUManager:                                "true",
		kubevirtv1.NodeSchedulable:                           "true",
	}

	tests := []struct {
		name            string
		vmAnnotations   map[string]string
		pod             *corev1.Pod
		createVM        bool
		expectedPatches types.PatchOps
	}{
		{
			name: "remove migration CPU selectors when annotation enabled",
			vmAnnotations: map[string]string{
				util.AnnotationRelaxCPUCompatibility: "true",
			},
			pod:      newMigrationPod(migrationCPUSelector),
			createVM: true,
			expectedPatches: func() types.PatchOps {
				keys := []string{
					kubevirtv1.CPUFeatureLabel + "aes",
					kubevirtv1.CPUModelLabel + "Skylake-Client",
					kubevirtv1.SupportedHostModelMigrationCPU + "x86_64",
					kubevirtv1.CPUModelVendorLabel + "Intel",
					kubevirtv1.HostModelCPULabel + "Skylake-Client",
					kubevirtv1.HostModelRequiredFeaturesLabel + "ssse3",
				}
				sort.Strings(keys)

				patches := make(types.PatchOps, 0, len(keys))
				for _, key := range keys {
					patches = append(patches, fmt.Sprintf(`{"op": "remove", "path": "/spec/nodeSelector/%s"}`, escapeJSONPointer(key)))
				}
				return patches
			}(),
		},
		{
			name: "skip when annotation is not enabled",
			vmAnnotations: map[string]string{
				util.AnnotationRelaxCPUCompatibility: "false",
			},
			pod:             newMigrationPod(migrationCPUSelector),
			createVM:        true,
			expectedPatches: nil,
		},
		{
			name: "skip when it is not a migration target pod",
			vmAnnotations: map[string]string{
				util.AnnotationRelaxCPUCompatibility: "true",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "virt-launcher-test",
					Namespace: "default",
					Labels: map[string]string{
						kubevirtv1.VirtualMachineNameLabel: "vm1",
					},
					Annotations: map[string]string{
						kubevirtv1.DomainAnnotation: "vm1",
					},
				},
				Spec: corev1.PodSpec{NodeSelector: migrationCPUSelector},
			},
			createVM:        true,
			expectedPatches: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientSet := fake.NewSimpleClientset()
			if tc.createVM {
				err := clientSet.Tracker().Add(&kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "vm1",
						Namespace:   "default",
						Annotations: tc.vmAnnotations,
					},
				})
				assert.NoError(t, err)
			}

			mutator := NewMutator(
				fakeclients.HarvesterSettingCache(clientSet.HarvesterhciV1beta1().Settings),
				fakeclients.VirtualMachineCache(clientSet.KubevirtV1().VirtualMachines),
			)

			patches, err := mutator.(*podMutator).patchMigrationCPUNodeSelector(tc.pod)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedPatches, patches)
		})
	}
}

func TestCreateWithRelaxCPUCompatibility(t *testing.T) {
	clientSet := fake.NewSimpleClientset()
	err := clientSet.Tracker().Add(&kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm1",
			Namespace: "default",
			Annotations: map[string]string{
				util.AnnotationRelaxCPUCompatibility: "true",
			},
		},
	})
	assert.NoError(t, err)

	mutator := NewMutator(
		fakeclients.HarvesterSettingCache(clientSet.HarvesterhciV1beta1().Settings),
		fakeclients.VirtualMachineCache(clientSet.KubevirtV1().VirtualMachines),
	)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "virt-launcher-test",
			Namespace: "default",
			Labels: map[string]string{
				kubevirtv1.MigrationJobLabel:       "migration-uid",
				kubevirtv1.VirtualMachineNameLabel: "vm1",
			},
			Annotations: map[string]string{
				kubevirtv1.DomainAnnotation: "vm1",
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				kubevirtv1.CPUFeatureLabel + "aes": "true",
			},
		},
	}

	patches, err := mutator.Create(nil, pod)
	assert.NoError(t, err)
	assert.Equal(t, types.PatchOps{
		fmt.Sprintf(`{"op": "remove", "path": "/spec/nodeSelector/%s"}`, escapeJSONPointer(kubevirtv1.CPUFeatureLabel+"aes")),
	}, patches)
}
