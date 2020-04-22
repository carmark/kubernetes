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

package cm

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/api/resource"
	internalapi "k8s.io/cri-api/pkg/apis"
	podresourcesapi "k8s.io/kubernetes/pkg/kubelet/apis/podresources/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	cputopology "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/devicemanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
)

type containerManagerStub struct {
	shouldResetExtendedResourceCapacity bool
	// Interface for exporting and allocating devices reported by device plugins.
	deviceManager devicemanager.Manager
	// Interface for Topology resource co-ordination
	topologyManager topologymanager.Manager
}

var _ ContainerManager = &containerManagerStub{}

func (cm *containerManagerStub) Start(node *v1.Node, activePods ActivePodsFunc, sourcesReady config.SourcesReady, podStatusProvider status.PodStatusProvider, _ internalapi.RuntimeService) error {
	klog.V(2).Infof("Starting stub container manager")
	// Starts device manager.
	if err := cm.deviceManager.Start(devicemanager.ActivePodsFunc(activePods), sourcesReady); err != nil {
		return err
	}
	return nil
}

func (cm *containerManagerStub) SystemCgroupsLimit() v1.ResourceList {
	return v1.ResourceList{}
}

func (cm *containerManagerStub) GetNodeConfig() NodeConfig {
	return NodeConfig{}
}

func (cm *containerManagerStub) GetMountedSubsystems() *CgroupSubsystems {
	return &CgroupSubsystems{}
}

func (cm *containerManagerStub) GetQOSContainersInfo() QOSContainersInfo {
	return QOSContainersInfo{}
}

func (cm *containerManagerStub) UpdateQOSCgroups() error {
	return nil
}

func (cm *containerManagerStub) Status() Status {
	return Status{}
}

func (cm *containerManagerStub) GetNodeAllocatableReservation() v1.ResourceList {
	return nil
}

func (cm *containerManagerStub) GetCapacity() v1.ResourceList {
	c := v1.ResourceList{
		v1.ResourceEphemeralStorage: *resource.NewQuantity(
			int64(0),
			resource.BinarySI),
	}
	return c
}

func (cm *containerManagerStub) GetPluginRegistrationHandler() cache.PluginHandler {
	return nil
}

func (cm *containerManagerStub) GetDevicePluginResourceCapacity() (v1.ResourceList, v1.ResourceList, []string) {
	return cm.deviceManager.GetCapacity()
}

func (cm *containerManagerStub) NewPodContainerManager() PodContainerManager {
	return &podContainerManagerStub{}
}

func (cm *containerManagerStub) GetResources(pod *v1.Pod, container *v1.Container) (*kubecontainer.RunContainerOptions, error) {
	opts := &kubecontainer.RunContainerOptions{}
	// Allocate should already be called during predicateAdmitHandler.Admit(),
	// just try to fetch device runtime information from cached state here
	devOpts, err := cm.deviceManager.GetDeviceRunContainerOptions(pod, container)
	if err != nil {
		return nil, err
	} else if devOpts == nil {
		return opts, nil
	}
	opts.Devices = append(opts.Devices, devOpts.Devices...)
	opts.Mounts = append(opts.Mounts, devOpts.Mounts...)
	opts.Envs = append(opts.Envs, devOpts.Envs...)
	opts.Annotations = append(opts.Annotations, devOpts.Annotations...)
	return opts, nil
}

func (cm *containerManagerStub) UpdatePluginResources(node *schedulernodeinfo.NodeInfo, attrs *lifecycle.PodAdmitAttributes) error {
	return cm.deviceManager.Allocate(node, attrs)
}

func (cm *containerManagerStub) InternalContainerLifecycle() InternalContainerLifecycle {
	return &internalContainerLifecycleImpl{cpumanager.NewFakeManager(), topologymanager.NewFakeManager()}
}

func (cm *containerManagerStub) GetPodCgroupRoot() string {
	return ""
}

func (cm *containerManagerStub) GetDevices(podUID, containerName string) []*podresourcesapi.ContainerDevices {
	return cm.deviceManager.GetDevices(podUID, containerName)
}

func (cm *containerManagerStub) ShouldResetExtendedResourceCapacity() bool {
	return cm.deviceManager.ShouldResetExtendedResourceCapacity()
}

func (cm *containerManagerStub) GetTopologyPodAdmitHandler() topologymanager.Manager {
	return cm.topologyManager
}

func NewStubContainerManager(devicePluginEnabled bool) ContainerManager {
	var cm = &containerManagerStub{shouldResetExtendedResourceCapacity: false}
	var err error
	cm.topologyManager = topologymanager.NewFakeManager()
	// Correct NUMA information is currently missing from cadvisor's
	// MachineInfo struct, so we use the CPUManager's internal logic for
	// gathering NUMANodeInfo to pass to components that care about it.
	numaNodeInfo, err := cputopology.GetNUMANodeInfo()
	if err != nil {
		klog.Error(err)
		return nil
	}

	klog.Infof("Creating device plugin manager: %t", devicePluginEnabled)
	if devicePluginEnabled {
		cm.deviceManager, err = devicemanager.NewManagerImpl(numaNodeInfo, cm.topologyManager)
		cm.topologyManager.AddHintProvider(cm.deviceManager)
	} else {
		cm.deviceManager, err = devicemanager.NewManagerStub()
	}
	return cm
}

func NewStubContainerManagerWithExtendedResource(shouldResetExtendedResourceCapacity bool) ContainerManager {
	return &containerManagerStub{shouldResetExtendedResourceCapacity: shouldResetExtendedResourceCapacity}
}
