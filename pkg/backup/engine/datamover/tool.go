// Package datamover implements the backup engine for backup types that move
// volume data to the remote backup target through a Harvester-managed Job
// (restic, kopia, ...).
//
// The Engine owns everything that is independent of the backup tool: the
// snapshot -> clone PVC -> Job lifecycle, credential Secret, snapshot check,
// forget on removal and progress reporting. A Tool plugs in only the parts that
// differ per tool: repository/runtime construction and the shell scripts that
// run inside the Job.
package datamover

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/settings"
)

// Runtime is the tool-specific, ready-to-use pod runtime shared by all Jobs the
// Engine creates (backup, snapshot check and forget).
type Runtime struct {
	Image        string
	Labels       map[string]string
	Env          []corev1.EnvVar
	Resources    corev1.ResourceRequirements
	VolumeMounts []corev1.VolumeMount
	Volumes      []corev1.Volume
	// Command is the tool CLI invocation prefix (binary plus global flags).
	Command string
}

// VolumesWith returns the runtime volumes followed by extra ones.
func (r *Runtime) VolumesWith(extra ...corev1.Volume) []corev1.Volume {
	result := append([]corev1.Volume{}, r.Volumes...)
	return append(result, extra...)
}

// Tool is the plug-in point of the data mover Engine.
type Tool interface {
	// Name identifies the tool. It is used as the prefix of every resource the
	// Engine creates (Jobs, credential Secret, clone PVC) and in messages.
	Name() string
	// BackupType is the VMBackup type served by this tool.
	BackupType() harvesterv1.BackupType
	// JobLabelKey is the label set to "true" on every Job of this tool, so the
	// Engine can map Job events back to the owning VMBackup.
	JobLabelKey() string
	// PasswordKey is the key of the repository password in the credential Secret.
	PasswordKey() string
	// SnapshotTags returns the tags that identify the snapshots of one volume.
	SnapshotTags(namespace, vmBackupName, volumeBackupName string) []string
	// NewRuntime builds the Job runtime for the backup target. hasCapacity is
	// false when the tool's concurrent Job limit is reached.
	NewRuntime(
		ctx context.Context,
		clientset kubernetes.Interface,
		secretName string,
		target *settings.BackupTarget,
	) (runtime *Runtime, hasCapacity bool, err error)
	// BackupScript streams the block device into the repository, creating the
	// repository first if needed.
	BackupScript(runtime *Runtime, devicePath, volumeName string, tags []string) string
	// CheckScript exits 0 if a snapshot matching tags exists, and
	// snapshotcheck.MissingExitCode if it does not.
	CheckScript(runtime *Runtime, tags []string) string
	// ForgetScript removes all snapshots matching tags from the repository.
	ForgetScript(runtime *Runtime, tags []string) string
}
