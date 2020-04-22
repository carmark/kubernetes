/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubemark

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	kubeletapp "k8s.io/kubernetes/cmd/kubelet/app"
	"k8s.io/kubernetes/cmd/kubelet/app/options"
	"k8s.io/kubernetes/pkg/kubelet"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cadvisor"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	kconfig "k8s.io/kubernetes/pkg/kubelet/config"
	containertest "k8s.io/kubernetes/pkg/kubelet/container/testing"
	"k8s.io/kubernetes/pkg/kubelet/dockershim"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/util/oom"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/cephfs"
	"k8s.io/kubernetes/pkg/volume/configmap"
	"k8s.io/kubernetes/pkg/volume/csi"
	"k8s.io/kubernetes/pkg/volume/downwardapi"
	"k8s.io/kubernetes/pkg/volume/emptydir"
	"k8s.io/kubernetes/pkg/volume/fc"
	"k8s.io/kubernetes/pkg/volume/flocker"
	"k8s.io/kubernetes/pkg/volume/git_repo"
	"k8s.io/kubernetes/pkg/volume/glusterfs"
	"k8s.io/kubernetes/pkg/volume/hostpath"
	"k8s.io/kubernetes/pkg/volume/iscsi"
	"k8s.io/kubernetes/pkg/volume/local"
	"k8s.io/kubernetes/pkg/volume/nfs"
	"k8s.io/kubernetes/pkg/volume/portworx"
	"k8s.io/kubernetes/pkg/volume/projected"
	"k8s.io/kubernetes/pkg/volume/quobyte"
	"k8s.io/kubernetes/pkg/volume/rbd"
	"k8s.io/kubernetes/pkg/volume/scaleio"
	"k8s.io/kubernetes/pkg/volume/secret"
	"k8s.io/kubernetes/pkg/volume/storageos"
	"k8s.io/kubernetes/pkg/volume/util/hostutil"
	"k8s.io/kubernetes/pkg/volume/util/subpath"
	"k8s.io/kubernetes/test/utils"

	"k8s.io/klog"
)

type HollowKubelet struct {
	KubeletFlags         *options.KubeletFlags
	KubeletConfiguration *kubeletconfig.KubeletConfiguration
	KubeletDeps          *kubelet.Dependencies
	pluginManager        pluginmanager.PluginManager
	// sourcesReady records the sources seen by the kubelet, it is thread-safe.
	sourcesReady kconfig.SourcesReady
}

func volumePlugins() []volume.VolumePlugin {
	allPlugins := []volume.VolumePlugin{}
	allPlugins = append(allPlugins, emptydir.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, git_repo.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, hostpath.ProbeVolumePlugins(volume.VolumeConfig{})...)
	allPlugins = append(allPlugins, nfs.ProbeVolumePlugins(volume.VolumeConfig{})...)
	allPlugins = append(allPlugins, secret.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, iscsi.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, glusterfs.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, rbd.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, quobyte.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, cephfs.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, downwardapi.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, fc.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, flocker.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, configmap.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, projected.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, portworx.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, scaleio.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, local.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, storageos.ProbeVolumePlugins()...)
	allPlugins = append(allPlugins, csi.ProbeVolumePlugins()...)
	return allPlugins
}

func NewHollowKubelet(
	nodeName string,
	flags *options.KubeletFlags,
	config *kubeletconfig.KubeletConfiguration,
	client *clientset.Clientset,
	heartbeatClient *clientset.Clientset,
	eventClient v1core.EventsGetter,
	cadvisorInterface cadvisor.Interface,
	dockerClientConfig *dockershim.ClientConfig,
	containerManager cm.ContainerManager,
	r record.EventRecorder) *HollowKubelet {
	d := &kubelet.Dependencies{
		KubeClient:         client,
		HeartbeatClient:    heartbeatClient,
		EventClient:        eventClient,
		DockerClientConfig: dockerClientConfig,
		CAdvisorInterface:  cadvisorInterface,
		Cloud:              nil,
		OSInterface:        &containertest.FakeOS{},
		ContainerManager:   containerManager,
		VolumePlugins:      volumePlugins(),
		TLSOptions:         nil,
		OOMAdjuster:        oom.NewFakeOOMAdjuster(),
		Mounter:            mount.New("" /* default mount path */),
		Subpather:          &subpath.FakeSubpath{},
		HostUtil:           hostutil.NewFakeHostUtil(nil),
		Recorder:           r,
	}
	cfg := kconfig.NewPodConfig(kconfig.PodConfigNotificationIncremental, r)
	var updatechannel chan<- interface{}
	if d.KubeClient != nil {
		klog.Infof("Watching apiserver")
		if updatechannel == nil {
			updatechannel = cfg.Channel(kubetypes.ApiserverSource)
		}
		kconfig.NewSourceApiserver(d.KubeClient, types.NodeName(nodeName), updatechannel)
	}
	d.PodConfig = cfg

	klog.V(2).Infof("Watch plugin in %s/%s or %s/%s", flags.RootDirectory, kconfig.DefaultKubeletPluginsRegistrationDirName, flags.RootDirectory, kconfig.DefaultKubeletPluginsDirName)
	os.Mkdir(flags.RootDirectory, 0755)
	pluginManager := pluginmanager.NewPluginManager(
		filepath.Join(flags.RootDirectory, kconfig.DefaultKubeletPluginsRegistrationDirName),
		filepath.Join(flags.RootDirectory, kconfig.DefaultKubeletPluginsDirName),
		d.Recorder,
	)
	sourcesReady := kconfig.NewSourcesReady(d.PodConfig.SeenAllSources)

	return &HollowKubelet{
		KubeletFlags:         flags,
		KubeletConfiguration: config,
		KubeletDeps:          d,
		pluginManager:        pluginManager,
		sourcesReady:         sourcesReady,
	}
}

// Starts this HollowKubelet and blocks.
func (hk *HollowKubelet) Run() {
	if err := kubeletapp.RunKubelet(&options.KubeletServer{
		KubeletFlags:         *hk.KubeletFlags,
		KubeletConfiguration: *hk.KubeletConfiguration,
	}, hk.KubeletDeps, false); err != nil {
		klog.Fatalf("Failed to run HollowKubelet: %v. Exiting.", err)
	}
	// containerManager must start after cAdvisor because it needs filesystem capacity information
	//if err := hk.KubeletDeps.ContainerManager.Start(nil, hk.GetActivePods, hk.sourcesReady, nil, nil); err != nil {
	// Fail kubelet and rely on the babysitter to retry starting kubelet.
	//	klog.Fatalf("Failed to start ContainerManager %v", err)
	//}
	// Adding Registration Callback function for Device Manager
	//hk.pluginManager.AddHandler(pluginwatcherapi.DevicePlugin, hk.KubeletDeps.ContainerManager.GetPluginRegistrationHandler())
	// Start the plugin manager
	//klog.V(4).Infof("starting plugin manager")
	//go hk.pluginManager.Run(hk.sourcesReady, wait.NeverStop)
	select {}
}

func (hk *HollowKubelet) GetActivePods() []*v1.Pod {
	//allPods := kl.podManager.GetPods()
	allPods := []*v1.Pod{}
	activePods := hk.filterOutTerminatedPods(allPods)
	return activePods
}

// filterOutTerminatedPods returns the given pods which the status manager
// does not consider failed or succeeded.
func (kl *HollowKubelet) filterOutTerminatedPods(pods []*v1.Pod) []*v1.Pod {
	var filteredPods []*v1.Pod
	for _, p := range pods {
		if kl.podIsTerminated(p) {
			continue
		}
		filteredPods = append(filteredPods, p)
	}
	return filteredPods
}

// podIsTerminated returns true if pod is in the terminated state ("Failed" or "Succeeded").
func (kl *HollowKubelet) podIsTerminated(pod *v1.Pod) bool {
	// Check the cached pod status which was set after the last sync.
	//status, ok := kl.statusManager.GetPodStatus(pod.UID)
	status := pod.Status
	//if !ok {
	// If there is no cached status, use the status from the
	// apiserver. This is useful if kubelet has recently been
	// restarted.
	// status = pod.Status
	//}
	return status.Phase == v1.PodFailed || status.Phase == v1.PodSucceeded || (pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses))
}

// notRunning returns true if every status is terminated or waiting, or the status list
// is empty.
func notRunning(statuses []v1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.State.Terminated == nil && status.State.Waiting == nil {
			return false
		}
	}
	return true
}

// Builds a KubeletConfiguration for the HollowKubelet, ensuring that the
// usual defaults are applied for fields we do not override.
func GetHollowKubeletConfig(
	nodeName string,
	kubeletPort int,
	kubeletReadOnlyPort int,
	maxPods int,
	podsPerCore int) (*options.KubeletFlags, *kubeletconfig.KubeletConfiguration) {

	testRootDir := utils.MakeTempDirOrDie("hollow-kubelet.", "")
	podFilePath := utils.MakeTempDirOrDie("static-pods", testRootDir)
	klog.Infof("Using %s as root dir for hollow-kubelet", testRootDir)

	// Flags struct
	f := options.NewKubeletFlags()
	f.EnableServer = true
	f.RootDirectory = testRootDir
	f.HostnameOverride = nodeName
	f.MinimumGCAge = metav1.Duration{Duration: 1 * time.Minute}
	f.MaxContainerCount = 100
	f.MaxPerPodContainerCount = 2
	f.RegisterNode = true
	f.RegisterSchedulable = true
	f.ProviderID = fmt.Sprintf("kubemark://%v", nodeName)

	// Config struct
	c, err := options.NewKubeletConfiguration()
	if err != nil {
		panic(err)
	}

	c.StaticPodURL = ""
	c.Address = "0.0.0.0" /* bind address */
	c.Port = int32(kubeletPort)
	c.ReadOnlyPort = int32(kubeletReadOnlyPort)
	c.StaticPodPath = podFilePath
	c.FileCheckFrequency.Duration = 20 * time.Second
	c.HTTPCheckFrequency.Duration = 20 * time.Second
	c.NodeStatusUpdateFrequency.Duration = 10 * time.Second
	c.NodeStatusReportFrequency.Duration = time.Minute
	c.SyncFrequency.Duration = 10 * time.Second
	c.EvictionPressureTransitionPeriod.Duration = 5 * time.Minute
	c.MaxPods = int32(maxPods)
	c.PodsPerCore = int32(podsPerCore)
	c.ClusterDNS = []string{}
	c.ImageGCHighThresholdPercent = 90
	c.ImageGCLowThresholdPercent = 80
	c.VolumeStatsAggPeriod.Duration = time.Minute
	c.CgroupRoot = ""
	c.CPUCFSQuota = true
	c.EnableControllerAttachDetach = false
	c.EnableDebuggingHandlers = true
	c.CgroupsPerQOS = false
	// hairpin-veth is used to allow hairpin packets. Note that this deviates from
	// what the "real" kubelet currently does, because there's no way to
	// set promiscuous mode on docker0.
	c.HairpinMode = kubeletconfig.HairpinVeth
	c.MaxOpenFiles = 1024
	c.RegistryBurst = 10
	c.RegistryPullQPS = 5.0
	c.ResolverConfig = kubetypes.ResolvConfDefault
	c.KubeletCgroups = "/kubelet"
	c.SerializeImagePulls = true
	c.SystemCgroups = ""
	c.ProtectKernelDefaults = false

	return f, c
}
