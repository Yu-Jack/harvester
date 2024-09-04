package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	helmv1 "github.com/k3s-io/helm-controller/pkg/apis/helm.cattle.io/v1"
	ctlhelmv1 "github.com/k3s-io/helm-controller/pkg/generated/controllers/helm.cattle.io"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/rancher/dynamiclistener"
	"github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	ctlcatalogv1 "github.com/rancher/rancher/pkg/generated/controllers/catalog.cattle.io"
	ctlrancherv3 "github.com/rancher/rancher/pkg/generated/controllers/management.cattle.io"
	ctlbatchv1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/batch"
	ctlcorev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/generic"
	"github.com/rancher/wrangler/v3/pkg/start"
	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/config"
	"github.com/harvester/harvester/pkg/controller/master/addon"
	"github.com/harvester/harvester/pkg/controller/master/mcmsettings"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io"
	"github.com/harvester/harvester/pkg/server"
	"github.com/harvester/harvester/tests/framework/cluster"
	"github.com/harvester/harvester/tests/framework/dsl"
	"github.com/harvester/harvester/tests/framework/helper"
	_ "github.com/harvester/harvester/tests/integration/api"
	"github.com/harvester/harvester/tests/integration/constant"
	_ "github.com/harvester/harvester/tests/integration/controllers"
	"github.com/harvester/harvester/tests/integration/controllers/fake"
	harvesterRuntime "github.com/harvester/harvester/tests/integration/runtime"
)

var (
	crdList = []string{
		"./controllers/manifest/helm-crd.yaml",
		"./controllers/manifest/app-crd.yaml",
		"./controllers/manifest/ranchersettings-crd.yaml",
		"./controllers/manifest/clusterrepos-crd.yaml",
		"../../deploy/charts/harvester-crd/templates/harvesterhci.io_addons.yaml",
	}
	scheme = runtime.NewScheme()
)

// Declarations for Ginkgo DSL
var Fail = ginkgo.Fail
var Describe = ginkgo.Describe
var It = ginkgo.It
var By = ginkgo.By
var BeforeEach = ginkgo.BeforeEach
var AfterEach = ginkgo.AfterEach
var RunSpecs = ginkgo.RunSpecs
var SynchronizedBeforeSuite = ginkgo.SynchronizedBeforeSuite
var SynchronizedAfterSuite = ginkgo.SynchronizedAfterSuite
var GinkgoWriter = ginkgo.GinkgoWriter
var GinkgoRecover = ginkgo.GinkgoRecover
var GinkgoT = ginkgo.GinkgoT
var Context = ginkgo.Context
var Specify = ginkgo.Specify

// Declarations for Gomega Matchers
var RegisterFailHandler = gomega.RegisterFailHandler
var Equal = gomega.Equal
var Expect = gomega.Expect
var BeNil = gomega.BeNil
var HaveOccurred = gomega.HaveOccurred
var BeEmpty = gomega.BeEmpty
var Eventually = gomega.Eventually
var BeEquivalentTo = gomega.BeEquivalentTo
var BeElementOf = gomega.BeElementOf
var Consistently = gomega.Consistently
var BeTrue = gomega.BeTrue

// Declarations for DSL
var MustNotError = dsl.MustNotError
var MustFinallyBeTrue = dsl.MustFinallyBeTrue
var MustRespCodeIs = dsl.MustRespCodeIs
var MustRespCodeIn = dsl.MustRespCodeIn
var MustEqual = dsl.MustEqual
var MustNotEqual = dsl.MustNotEqual
var Cleanup = dsl.Cleanup
var CheckRespCodeIs = dsl.CheckRespCodeIs
var HasNoneVMI = dsl.HasNoneVMI
var AfterVMRunning = dsl.AfterVMRunning
var AfterVMIRunning = dsl.AfterVMIRunning
var AfterVMIRestarted = dsl.AfterVMIRestarted
var MustVMPaused = dsl.MustVMPaused
var MustVMRunning = dsl.MustVMRunning
var MustVMDeleted = dsl.MustVMDeleted
var MustVMIRunning = dsl.MustVMIRunning
var MustPVCDeleted = dsl.MustPVCDeleted

func TestAPI(t *testing.T) {
	defer ginkgo.GinkgoRecover()

	gomega.RegisterFailHandler(ginkgo.Fail)

	ginkgo.RunSpecs(t, "api suite")
}

var _ = SynchronizedBeforeSuite(
	func() []byte {
		// Only first processor run this function
		constant.TestCtx, constant.TestCtxCancel = context.WithCancel(context.Background())
		var err error

		By("starting harvester test cluster")
		constant.HarvesterKubeClientConfig, constant.TestHarvesterCluster, err = cluster.Start(GinkgoWriter)
		MustNotError(err)

		constant.HarvesterKubeConfig, err = constant.HarvesterKubeClientConfig.ClientConfig()
		MustNotError(err)

		// first part
		By("construct harvester runtime")
		err = harvesterRuntime.Construct(constant.TestCtx, constant.HarvesterKubeConfig)
		MustNotError(err)

		By("set harvester config")
		constant.HarvesterOptions, err = harvesterRuntime.SetConfig()
		MustNotError(err)

		By("new harvester server")
		constant.Harvester, err = server.New(constant.TestCtx, constant.HarvesterKubeClientConfig, constant.HarvesterOptions)
		MustNotError(err)

		By("start harvester server")
		listenOpts := &dynamiclistener.Config{
			CloseConnOnCertChange: false,
		}
		constant.TestSuiteStartErrChan = make(chan error)
		go func() {
			constant.TestSuiteStartErrChan <- constant.Harvester.ListenAndServe(listenOpts, constant.HarvesterOptions)
		}()

		// NB(thxCode): since the start of all controllers is not synchronized,
		// it cannot guarantee the controllers has been start,
		// which means the cache(informer) has not ready,
		// so we give a stupid time sleep to trigger the first list-watch,
		// and please use the client interface instead of informer interface if you can.
		select {
		case <-time.After(constant.HarvesterStartTimeOut * time.Second):
			MustFinallyBeTrue(func() bool {
				return validateAPIIsReady()
			})
		case err := <-constant.TestSuiteStartErrChan:
			MustNotError(err)
		}

		// second part
		By("starting pure test cluster")
		constant.PureClusterKubeClientConfig, constant.TestPureCluster, err = cluster.Start(GinkgoWriter)
		MustNotError(err)

		constant.PureClusterKubeConfig, err = constant.PureClusterKubeClientConfig.ClientConfig()
		MustNotError(err)

		ginkgo.By("install crds")
		var crds []apiextensionsv1.CustomResourceDefinition

		for _, v := range crdList {
			objs, err := generateObjects(v)
			dsl.MustNotError(err)
			crds = append(crds, objs)
		}
		err = applyObj(crds)
		dsl.MustNotError(err)

		err = helmv1.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = harvesterv1.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = batchv1.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = catalogv1.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = managementv3.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = corev1.AddToScheme(scheme)
		dsl.MustNotError(err)

		err = catalogv1.AddToScheme(scheme)
		dsl.MustNotError(err)

		clientFactory, err := client.NewSharedClientFactory(constant.PureClusterKubeConfig, nil)
		dsl.MustNotError(err)

		cacheFactory := cache.NewSharedCachedFactory(clientFactory, nil)
		scf := controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{})

		factoryOpts := &generic.FactoryOptions{
			SharedControllerFactory: scf,
		}

		constant.TestCtx, constant.Scaled, err = config.SetupScaled(constant.TestCtx, constant.PureClusterKubeConfig, factoryOpts)
		dsl.MustNotError(err)

		err = startControllers(constant.TestCtx, constant.PureClusterKubeConfig, factoryOpts)
		dsl.MustNotError(err)

		harvesterRawConf, err := constant.HarvesterKubeClientConfig.RawConfig()
		MustNotError(err)

		pureClusterRawConf, err := constant.PureClusterKubeClientConfig.RawConfig()
		MustNotError(err)

		combinedConfig := constant.CombinedConfig{HarvesterRawKubeConfig: harvesterRawConf, PureRawKubeConfig: pureClusterRawConf, Options: constant.HarvesterOptions}

		b, err := json.Marshal(combinedConfig)
		MustNotError(err)
		return b
	}, func(combinedConf []byte) {
		// All processors run this function

		// combinedConf is the return value from previous function
		err := json.Unmarshal(combinedConf, &constant.CombinedCfg)
		MustNotError(err)

		constant.HarvesterOptions = constant.CombinedCfg.Options

		constant.HarvesterKubeConfig, err = clientcmd.NewDefaultClientConfig(constant.CombinedCfg.HarvesterRawKubeConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
		MustNotError(err)

		constant.PureClusterKubeConfig, err = clientcmd.NewDefaultClientConfig(constant.CombinedCfg.PureRawKubeConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
		MustNotError(err)

		//constant.TestCtx = context.TODO()
	})

var _ = SynchronizedAfterSuite(func() {
	// All processors run this function
}, func() {
	// Only first processor run this function
	By("tearing down harvester runtime")
	err := harvesterRuntime.Destruct(context.Background(), constant.HarvesterKubeConfig)
	MustNotError(err)

	By("tearing down both test cluster")
	err = cluster.Stop(GinkgoWriter)
	MustNotError(err)

	By("tearing down harvester server")
	if constant.TestCtxCancel != nil {
		constant.TestCtxCancel()
	}
})

// validate the v1 api server is ready
func validateAPIIsReady() bool {
	apiURL := helper.BuildAPIURL("v1", "", constant.HarvesterOptions.HTTPSListenPort)
	code, _, err := helper.GetResponse(apiURL)
	if err != nil || code != http.StatusOK {
		logrus.Errorf("failed to get %s, error: %d", apiURL, err)
		return false
	}
	return true
}

func generateObjects(fileName string) (apiextensionsv1.CustomResourceDefinition, error) {
	var result apiextensionsv1.CustomResourceDefinition
	contentBytes, err := os.ReadFile(fileName)
	if err != nil {
		return result, err
	}

	err = yaml.Unmarshal(contentBytes, &result)
	if err != nil {
		return result, err
	}
	return result, nil
}

func applyObj(obj []apiextensionsv1.CustomResourceDefinition) error {
	apiClient, err := apiextensionsclient.NewForConfig(constant.PureClusterKubeConfig)
	if err != nil {
		return err
	}

	for i := range obj {
		if _, err := apiClient.ApiextensionsV1().CustomResourceDefinitions().Create(constant.TestCtx, &obj[i], metav1.CreateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func startControllers(ctx context.Context, restConfig *rest.Config, opts *ctlharvesterv1.FactoryOptions) error {

	// to speed up testing, override default backofflimit for jobs
	harvesterv1.DefaultJobBackOffLimit = 1

	harvesterFactory, err := ctlharvesterv1.NewFactoryFromConfigWithOptions(restConfig, opts)

	if err != nil {
		return err
	}

	core, err := ctlcorev1.NewFactoryFromConfigWithOptions(restConfig, opts)
	if err != nil {
		return err
	}

	batch, err := ctlbatchv1.NewFactoryFromConfigWithOptions(restConfig, opts)
	if err != nil {
		return err
	}

	helm, err := ctlhelmv1.NewFactoryFromConfigWithOptions(restConfig, opts)
	if err != nil {
		return err
	}

	catalog, err := ctlcatalogv1.NewFactoryFromConfigWithOptions(restConfig, opts)
	if err != nil {
		return err
	}

	rancher, err := ctlrancherv3.NewFactoryFromConfigWithOptions(restConfig, opts)
	if err != nil {
		return err
	}

	m := &config.Management{
		HarvesterFactory:         harvesterFactory,
		CoreFactory:              core,
		BatchFactory:             batch,
		HelmFactory:              helm,
		CatalogFactory:           catalog,
		RancherManagementFactory: rancher,
	}

	_ = batch.ControllerFactory().SharedCacheFactory().WaitForCacheSync(ctx)

	if err = addon.Register(constant.TestCtx, m, config.Options{}); err != nil {
		return err
	}

	if err = mcmsettings.Register(constant.TestCtx, m, config.Options{}); err != nil {
		return err
	}

	if err = fake.RegisterFakeControllers(constant.TestCtx, m, config.Options{}); err != nil {
		return err
	}

	logrus.Infof("sync status of batch informer: %v", batch.Batch().V1().Job().Informer().HasSynced())
	if err = start.All(ctx, 10, harvesterFactory, core, batch, helm, catalog, rancher); err != nil {
		return err
	}

	for !batch.Batch().V1().Job().Informer().HasSynced() {
		time.Sleep(5 * time.Second)
	}

	return nil
}
