package datamover

import (
	"context"
	"errors"
	"fmt"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	ctlbatchv1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/batch/v1"
	ctlcorev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	ctlstoragev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/storage/v1"
	wranglername "github.com/rancher/wrangler/v3/pkg/name"
	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/backup/common"
	"github.com/harvester/harvester/pkg/backup/engine"
	ctlsnapshotv1 "github.com/harvester/harvester/pkg/generated/controllers/snapshot.storage.k8s.io/v1"
	"github.com/harvester/harvester/pkg/util"
	backuputil "github.com/harvester/harvester/pkg/util/backup"
	"github.com/harvester/harvester/pkg/util/jobprogress"
	"github.com/harvester/harvester/pkg/util/snapshotcheck"
)

const (
	vmBackupKind       = "VirtualMachineBackup"
	labelValueTrue     = "true"
	checkContainerName = "check"
)

var (
	_ engine.BackupEngine   = (*Engine)(nil)
	_ engine.BackupPreparer = (*Engine)(nil)
)

// Engine is the backup engine shared by all data mover tools.
type Engine struct {
	tool          Tool
	vmbo          common.VMBackupOperator
	vsHelper      *common.VolumeSnapshotHelper
	vsClient      ctlsnapshotv1.VolumeSnapshotClient
	pvcCache      ctlcorev1.PersistentVolumeClaimCache
	pvcClient     ctlcorev1.PersistentVolumeClaimClient
	scCache       ctlstoragev1.StorageClassCache
	secretCache   ctlcorev1.SecretCache
	secretClient  ctlcorev1.SecretClient
	jobController ctlbatchv1.JobController
	jobCache      ctlbatchv1.JobCache
	jobClient     ctlbatchv1.JobClient
	clientset     kubernetes.Interface
}

func NewEngine(
	tool Tool,
	vmbo common.VMBackupOperator,
	vsCache ctlsnapshotv1.VolumeSnapshotCache,
	vsClient ctlsnapshotv1.VolumeSnapshotClient,
	pvcCache ctlcorev1.PersistentVolumeClaimCache,
	pvcClient ctlcorev1.PersistentVolumeClaimClient,
	secretCache ctlcorev1.SecretCache,
	secretClient ctlcorev1.SecretClient,
	scCache ctlstoragev1.StorageClassCache,
	jobController ctlbatchv1.JobController,
	clientset kubernetes.Interface,
) engine.BackupEngine {
	return &Engine{
		tool:          tool,
		vmbo:          vmbo,
		vsHelper:      common.NewVolumeSnapshotHelper(vsCache, vsClient, nil, nil, vmbo, pvcCache, scCache),
		vsClient:      vsClient,
		pvcCache:      pvcCache,
		pvcClient:     pvcClient,
		scCache:       scCache,
		secretCache:   secretCache,
		secretClient:  secretClient,
		jobController: jobController,
		jobCache:      jobController.Cache(),
		jobClient:     jobController,
		clientset:     clientset,
	}
}

// RegisterWatchers maps Job changes back to the owning VMBackup and enqueues
// it, removing polling latency when a backup, check or forget Job changes.
func (e *Engine) RegisterWatchers(ctx context.Context, enqueue func(namespace, name string)) {
	e.jobController.OnChange(ctx, e.tool.Name()+"-backup-job-watcher", func(_ string, job *batchv1.Job) (*batchv1.Job, error) {
		if job == nil {
			return nil, nil
		}
		if job.Labels[e.tool.JobLabelKey()] != labelValueTrue {
			return job, nil
		}
		owner := util.FindOwnerReference(job, harvesterv1.SchemeGroupVersion.String(), vmBackupKind)
		if owner != nil {
			enqueue(job.Namespace, owner.Name)
		}
		return job, nil
	})
}

func (e *Engine) Reconcile(
	vmb *harvesterv1.VirtualMachineBackup,
	volIndex int,
	_ map[string]snapshotv1.VolumeSnapshotClass,
) error {
	vb := e.vmbo.GetVolBackup(vmb, volIndex)
	if vb == nil {
		return fmt.Errorf("volume backup at index %d not found", volIndex)
	}
	if e.vmbo.GetVolBackupReadyToUse(vb) {
		// Backup is persisted in S3; the temporary clone PVC and VolumeSnapshot
		// are no longer needed. Cleanup is idempotent, so returning an error keeps
		// the VMBackup non-ready and lets the controller retry it.
		return e.cleanupTemporaryResources(vmb, vb)
	}
	if ready, err := e.checkRemoteSnapshot(vmb, vb); ready || err != nil {
		return err
	}
	return e.reconcileBackupJob(vmb, vb)
}

func (e *Engine) Prepare(
	vmb *harvesterv1.VirtualMachineBackup,
	vsClassMap map[string]snapshotv1.VolumeSnapshotClass,
) (bool, error) {
	if e.isDiscoveredBackup(vmb) {
		return true, nil
	}
	return e.vsHelper.PrepareSnapshots(vmb, vsClassMap)
}

func (e *Engine) reconcileBackupJob(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup) error {
	jobName, err := e.jobName(vb)
	if err != nil {
		return err
	}
	job, err := e.jobCache.Get(e.vmbo.GetNamespace(vmb), jobName)
	if err == nil {
		return e.syncFromJob(vb, job)
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	clonePVCName := e.clonePVCName(vmb, vb)
	if err := e.ensureClonePVC(vmb, vb, clonePVCName); err != nil {
		return err
	}
	if err := e.createBackupJob(vmb, vb, clonePVCName, jobName); err != nil {
		return err
	}
	return engine.ErrRetryLater
}

func (e *Engine) UpdateProgress(vb *harvesterv1.VolumeBackup) (int64, error) {
	if e.vmbo.GetVolBackupReadyToUse(vb) {
		return 100, nil
	}
	jobName, jobNamespace, ok := e.jobLocator(vb)
	if !ok {
		return int64(e.vmbo.GetVolBackupProgress(vb)), nil
	}
	current := e.vmbo.GetVolBackupProgress(vb)
	pct, err := jobprogress.FetchProgressPercent(context.Background(), e.clientset, jobNamespace, jobName)
	if err != nil {
		return int64(current), nil
	}
	if err := e.vmbo.SetVolBackupProgress(vb, pct); err != nil {
		return int64(current), nil
	}
	return int64(pct), nil
}

// ForceDelete drives the per-volume cleanup via the same reconcile pattern as
// Reconcile: each call checks the forget Job's state and either creates it,
// returns ErrRetryLater to await completion, or returns nil to let the caller
// remove finalizers. Once nil is returned the VMBackup CR is finalized and
// K8s cascade-GC reaps the backup Job, clone PVC, and VolumeSnapshot via
// their VMBackup ownerRefs — no explicit cleanup needed here.
func (e *Engine) ForceDelete(vmb *harvesterv1.VirtualMachineBackup, volIndex int) error {
	vb := e.vmbo.GetVolBackup(vmb, volIndex)
	if vb == nil {
		return nil
	}
	forgetName, err := e.forgetJobName(vb)
	if err != nil {
		return err
	}
	namespace := e.vmbo.GetNamespace(vmb)
	job, err := e.jobCache.Get(namespace, forgetName)
	if err == nil {
		return e.syncFromForgetJob(job)
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if err := e.createForgetJob(vmb, vb, forgetName); err != nil {
		if errors.Is(err, engine.ErrRetryLater) {
			return err
		}
		return fmt.Errorf("failed to create %s forget job %s/%s: %w", e.tool.Name(), namespace, forgetName, err)
	}
	return engine.ErrRetryLater
}

// syncFromForgetJob retains successful Jobs as per-volume cleanup checkpoints.
// A failed pod is not terminal while the Job still has retries available.
// Terminal failures are removed so requeue can create a fresh attempt without
// depending on the TTL controller.
func (e *Engine) syncFromForgetJob(job *batchv1.Job) error {
	if job.Status.Succeeded > 0 {
		return nil
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type != batchv1.JobFailed || condition.Status != corev1.ConditionTrue {
			continue
		}
		if err := e.jobClient.Delete(job.Namespace, job.Name, &metav1.DeleteOptions{
			PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
			Preconditions:     &metav1.Preconditions{UID: &job.UID},
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return fmt.Errorf("%s forget job %s/%s failed; retrying cleanup", e.tool.Name(), job.Namespace, job.Name)
	}
	return engine.ErrRetryLater
}

func (e *Engine) ensureClonePVC(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup, clonePVCName string) error {
	namespace := e.vmbo.GetNamespace(vmb)
	pvc, err := e.pvcCache.Get(namespace, clonePVCName)
	if err == nil {
		// WaitForFirstConsumer storage classes only bind once a pod (the backup Job)
		// mounts the PVC, so gating here would deadlock that path.
		if e.waitsForImmediateBinding(pvc) && pvc.Status.Phase != corev1.ClaimBound {
			return engine.ErrRetryLater
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	pvc = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:            clonePVCName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{e.vsHelper.BuildOwnerReference(vmb)},
		},
		Spec: e.vmbo.GetVolBackupPVCSpec(vb),
	}
	pvc.Spec.VolumeName = ""
	pvc.Spec.DataSource = &corev1.TypedLocalObjectReference{
		APIGroup: ptr.To("snapshot.storage.k8s.io"),
		Kind:     "VolumeSnapshot",
		Name:     e.volumeSnapshotName(vb),
	}
	pvc.Spec.DataSourceRef = nil
	_, err = e.pvcClient.Create(pvc)
	if apierrors.IsAlreadyExists(err) {
		return engine.ErrRetryLater
	}
	return err
}

// waitsForImmediateBinding reports whether the PVC's StorageClass binds eagerly,
// in which case ClaimBound is reachable without a consumer pod. If the SC is
// missing or unreadable we conservatively assume Immediate (the K8s default).
func (e *Engine) waitsForImmediateBinding(pvc *corev1.PersistentVolumeClaim) bool {
	scName := ptr.Deref(pvc.Spec.StorageClassName, "")
	if scName == "" {
		return true
	}
	sc, err := e.scCache.Get(scName)
	if err != nil || sc.VolumeBindingMode == nil {
		return true
	}
	return *sc.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer
}

// newRuntime ensures the credential Secret exists and builds the tool runtime.
// It returns engine.ErrRetryLater when the tool's Job limit is reached.
func (e *Engine) newRuntime(vmb *harvesterv1.VirtualMachineBackup) (*Runtime, error) {
	target, err := backuputil.TargetFromStatus(e.vmbo.GetBackupTarget(vmb))
	if err != nil {
		return nil, err
	}
	secretName, err := e.ensureSecret(vmb)
	if err != nil {
		return nil, err
	}
	runtime, hasCapacity, err := e.tool.NewRuntime(context.Background(), e.clientset, secretName, target)
	if err != nil {
		return nil, err
	}
	if !hasCapacity {
		return nil, engine.ErrRetryLater
	}
	return runtime, nil
}

func (e *Engine) snapshotTags(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup) ([]string, error) {
	vbName := e.vmbo.GetVolBackupName(vb)
	if vbName == nil || *vbName == "" {
		return nil, fmt.Errorf("volume backup name is nil")
	}
	return e.tool.SnapshotTags(e.vmbo.GetNamespace(vmb), e.vmbo.GetName(vmb), *vbName), nil
}

func (e *Engine) createBackupJob(
	vmb *harvesterv1.VirtualMachineBackup,
	vb *harvesterv1.VolumeBackup,
	clonePVCName, jobName string,
) error {
	tags, err := e.snapshotTags(vmb, vb)
	if err != nil {
		return err
	}
	runtime, err := e.newRuntime(vmb)
	if err != nil {
		return err
	}

	devicePath := "/dev/" + clonePVCName
	script := e.tool.BackupScript(runtime, devicePath, e.vmbo.GetVolBackupPVName(vb), tags)
	volumes := runtime.VolumesWith(corev1.Volume{
		Name: "volume",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: clonePVCName},
		},
	})
	job := &batchv1.Job{
		ObjectMeta: e.jobMeta(vmb, jobName, runtime),
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To[int32](60),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: runtime.Labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:         "backup",
						Image:        runtime.Image,
						Env:          runtime.Env,
						Command:      []string{"/bin/sh", "-c"},
						Resources:    runtime.Resources,
						VolumeMounts: runtime.VolumeMounts,
						Args:         []string{script},
						VolumeDevices: []corev1.VolumeDevice{{
							Name:       "volume",
							DevicePath: devicePath,
						}},
					}},
					Volumes: volumes,
				},
			},
		},
	}
	_, err = e.jobClient.Create(job)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (e *Engine) checkRemoteSnapshot(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup) (bool, error) {
	checkName, err := e.checkJobName(vb)
	if err != nil {
		return false, err
	}
	namespace := e.vmbo.GetNamespace(vmb)
	result, err := snapshotcheck.CheckJob(context.Background(), e.clientset, snapshotcheck.CheckJobOptions{
		EngineName:      e.tool.Name(),
		ContainerName:   checkContainerName,
		MissingExitCode: snapshotcheck.MissingExitCode,
		JobName:         checkName,
		GetJob: func(name string) (*batchv1.Job, error) {
			return e.jobCache.Get(namespace, name)
		},
		CreateJob: func(name string) error {
			return e.createCheckJob(vmb, vb, name)
		},
	})
	if err != nil {
		return false, err
	}
	discoveredBackup := e.isDiscoveredBackup(vmb)
	switch result {
	case snapshotcheck.Pending:
		return false, engine.ErrRetryLater
	case snapshotcheck.Missing:
		if discoveredBackup {
			return false, fmt.Errorf("%s snapshot for discovered backup %s/%s not found", e.tool.Name(), namespace, checkName)
		}
		return false, nil
	case snapshotcheck.Failed:
		if discoveredBackup {
			return false, fmt.Errorf("%s snapshot check job %s/%s failed", e.tool.Name(), namespace, checkName)
		}
		return false, nil
	case snapshotcheck.Found:
		return e.markReady(vb)
	default:
		return false, fmt.Errorf("unknown %s snapshot check result %q", e.tool.Name(), result)
	}
}

func (e *Engine) isDiscoveredBackup(vmb *harvesterv1.VirtualMachineBackup) bool {
	return e.vmbo.GetSourceUID(vmb) == nil
}

func (e *Engine) markReady(vb *harvesterv1.VolumeBackup) (bool, error) {
	if err := e.setVolBackupReady(vb); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) setVolBackupReady(vb *harvesterv1.VolumeBackup) error {
	now := metav1.Now()
	ready := true
	if err := e.vmbo.SetVolBackupReadyToUse(vb, &ready); err != nil {
		return err
	}
	if err := e.vmbo.SetVolBackupCreationTime(vb, &now); err != nil {
		return err
	}
	return e.vmbo.SetVolBackupProgress(vb, 100)
}

func (e *Engine) createCheckJob(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup, jobName string) error {
	tags, err := e.snapshotTags(vmb, vb)
	if err != nil {
		return err
	}
	runtime, err := e.newRuntime(vmb)
	if err != nil {
		return err
	}
	job := &batchv1.Job{
		ObjectMeta: e.jobMeta(vmb, jobName, runtime),
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To[int32](60),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: runtime.Labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:         checkContainerName,
						Image:        runtime.Image,
						Env:          runtime.Env,
						Command:      []string{"/bin/sh", "-c"},
						Resources:    runtime.Resources,
						VolumeMounts: runtime.VolumeMounts,
						Args:         []string{e.tool.CheckScript(runtime, tags)},
					}},
					Volumes: runtime.Volumes,
				},
			},
		},
	}
	_, err = e.jobClient.Create(job)
	if apierrors.IsAlreadyExists(err) {
		return engine.ErrRetryLater
	}
	return err
}

// createForgetJob fires a one-shot Job that removes the snapshots matching this
// VolumeBackup from the repository. It uses the per-VMBackup credential Secret;
// Pod env from secretKeyRef is resolved at startup, so the Pod survives the
// Secret being GC'd once the VMBackup CR is finalized.
func (e *Engine) createForgetJob(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup, jobName string) error {
	tags, err := e.snapshotTags(vmb, vb)
	if err != nil {
		return err
	}
	runtime, err := e.newRuntime(vmb)
	if err != nil {
		return err
	}
	job := &batchv1.Job{
		ObjectMeta: e.jobMeta(vmb, jobName, runtime),
		Spec: batchv1.JobSpec{
			// Keep successful per-volume Jobs until the owning VMBackup is
			// finalized; otherwise a slow next volume can outlive their TTL
			// and cause completed cleanup to be repeated indefinitely.
			BackoffLimit: ptr.To[int32](3),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: runtime.Labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:         "forget",
						Image:        runtime.Image,
						Env:          runtime.Env,
						Command:      []string{"/bin/sh", "-c"},
						Resources:    runtime.Resources,
						VolumeMounts: runtime.VolumeMounts,
						Args:         []string{e.tool.ForgetScript(runtime, tags)},
					}},
					Volumes: runtime.Volumes,
				},
			},
		},
	}
	_, err = e.jobClient.Create(job)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (e *Engine) jobMeta(vmb *harvesterv1.VirtualMachineBackup, name string, runtime *Runtime) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:            name,
		Namespace:       e.vmbo.GetNamespace(vmb),
		Labels:          runtime.Labels,
		OwnerReferences: []metav1.OwnerReference{e.vsHelper.BuildOwnerReference(vmb)},
	}
}

// ensureSecret copies the S3 credentials of the backup target into a
// per-VMBackup Secret together with the repository password and returns its name.
func (e *Engine) ensureSecret(vmb *harvesterv1.VirtualMachineBackup) (string, error) {
	source, err := e.secretCache.Get(util.LonghornSystemNamespaceName, util.BackupTargetSecretName)
	if err != nil {
		return "", err
	}
	backupTarget := e.vmbo.GetBackupTarget(vmb)
	if backupTarget == nil {
		return "", fmt.Errorf("VMBackup does not record a backup target")
	}
	password, err := backuputil.RepositoryPassword(backupTarget.RepositoryID, e.tool.BackupType())
	if err != nil {
		return "", err
	}

	name := e.credentialsName(vmb)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       e.vmbo.GetNamespace(vmb),
			OwnerReferences: []metav1.OwnerReference{e.vsHelper.BuildOwnerReference(vmb)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			util.AWSAccessKey:    source.Data[util.AWSAccessKey],
			util.AWSSecretKey:    source.Data[util.AWSSecretKey],
			e.tool.PasswordKey(): []byte(password),
		},
	}
	if _, err := e.secretClient.Create(secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return name, nil
}

func (e *Engine) syncFromJob(vb *harvesterv1.VolumeBackup, job *batchv1.Job) error {
	if job.Status.Failed > 0 {
		return fmt.Errorf("%s backup job %s/%s failed", e.tool.Name(), job.Namespace, job.Name)
	}
	if job.Status.Succeeded == 0 {
		// Job still running — update progress from io-mode pod logs (best effort).
		if pct, err := jobprogress.FetchProgressPercent(context.Background(), e.clientset, job.Namespace, job.Name); err == nil {
			_ = e.vmbo.SetVolBackupProgress(vb, pct)
		}
		return engine.ErrRetryLater
	}
	if err := e.setVolBackupReady(vb); err != nil {
		return err
	}
	// Cleanup is deferred to the next reconcile (when ReadyToUse is persisted)
	// to avoid a race where Update fails after we've deleted the VS/clone PVC.
	logrus.Debugf("%s backup job %s/%s completed", e.tool.Name(), job.Namespace, job.Name)
	return nil
}

// cleanupTemporaryResources removes the backup Job, cloned PVC, and
// VolumeSnapshot created for this backup. Called after ReadyToUse is
// persisted, so the Job has already succeeded (otherwise we wouldn't be
// here) and failure of any step is safe to retry on the next reconcile.
//
// Order matters: delete the Job first so its Pod is GC'd (Background
// propagation), otherwise the clone PVC would stay stuck on the
// kubernetes.io/pvc-protection finalizer until TTLSecondsAfterFinished
// reaped the Pod about a minute later.
func (e *Engine) cleanupTemporaryResources(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup) error {
	namespace := e.vmbo.GetNamespace(vmb)
	return errors.Join(
		e.deleteBackupJob(namespace, vb),
		e.deleteClonePVC(namespace, e.clonePVCName(vmb, vb)),
		e.deleteSnapshot(namespace, e.volumeSnapshotName(vb)),
	)
}

func (e *Engine) deleteBackupJob(namespace string, vb *harvesterv1.VolumeBackup) error {
	jobName, err := e.jobName(vb)
	if err != nil {
		return err
	}
	err = e.jobClient.Delete(namespace, jobName, &metav1.DeleteOptions{
		PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
	})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("failed to delete %s backup job %s/%s: %w", e.tool.Name(), namespace, jobName, err)
}

func (e *Engine) deleteClonePVC(namespace, name string) error {
	err := e.pvcClient.Delete(namespace, name, &metav1.DeleteOptions{})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("failed to delete %s temporary PVC %s/%s: %w", e.tool.Name(), namespace, name, err)
}

func (e *Engine) deleteSnapshot(namespace, name string) error {
	if name == "" {
		return nil
	}
	err := e.vsClient.Delete(namespace, name, &metav1.DeleteOptions{})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("failed to delete %s temporary VolumeSnapshot %s/%s: %w", e.tool.Name(), namespace, name, err)
}

func (e *Engine) volumeSnapshotName(vb *harvesterv1.VolumeBackup) string {
	if name := e.vmbo.GetVolBackupName(vb); name != nil {
		return *name
	}
	return ""
}

func (e *Engine) credentialsName(vmb *harvesterv1.VirtualMachineBackup) string {
	return wranglername.SafeConcatName(e.tool.Name()+"-credentials", e.vmbo.GetName(vmb))
}

func (e *Engine) clonePVCName(vmb *harvesterv1.VirtualMachineBackup, vb *harvesterv1.VolumeBackup) string {
	return wranglername.SafeConcatName(
		e.vmbo.GetName(vmb),
		e.vmbo.GetVolBackupPVCName(vb),
		e.tool.Name()+"-clone",
	)
}

func (e *Engine) volumeJobName(prefix string, vb *harvesterv1.VolumeBackup) (string, error) {
	name := e.vmbo.GetVolBackupName(vb)
	if name == nil {
		return "", fmt.Errorf("volume backup name is nil")
	}
	return wranglername.SafeConcatName(e.tool.Name()+"-"+prefix, *name), nil
}

func (e *Engine) jobName(vb *harvesterv1.VolumeBackup) (string, error) {
	return e.volumeJobName("backup", vb)
}

func (e *Engine) forgetJobName(vb *harvesterv1.VolumeBackup) (string, error) {
	return e.volumeJobName("forget", vb)
}

func (e *Engine) checkJobName(vb *harvesterv1.VolumeBackup) (string, error) {
	return e.volumeJobName("check", vb)
}

// jobLocator returns the name/namespace to look up the backup Job for vb,
// or false if the volume backup name isn't set yet. Used by UpdateProgress
// which has only a VolumeBackup handle (no enclosing VMBackup). The Job is
// created in the same namespace as the source PVC.
func (e *Engine) jobLocator(vb *harvesterv1.VolumeBackup) (name, namespace string, ok bool) {
	name, err := e.jobName(vb)
	if err != nil {
		return "", "", false
	}
	namespace = e.vmbo.GetVolBackupPVCNameSpace(vb)
	if namespace == "" {
		return "", "", false
	}
	return name, namespace, true
}
