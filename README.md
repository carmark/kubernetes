# Kubernetes simulation

We use this code to simulate the kubernetes cluster with GPU and/or RDMA.

## How-to

#### Build kubemark

```
# Clone source code of this repo
$ cd cmd/kubemark/
$ go build .
```

#### Run kubemark

```
$ export DEVICE_PLUGIN_PATH=/tmp/var/lib/kubelet/device-plugins/
$ export ND_MEAN=20; export ND_DEV=2
$ ./kubemark --kubelet-port 10350 --kubeconfig /etc/kubernetes/admin.conf --morph kubelet --name fake-node --kubelet-read-only-port 10355
```

#### Run Mocked Device Plugin

```
# clone https://git.code.oa.com/kubeflow/k8s-device-plugin
$ cd k8s-device-plugin & go build .
$ export DEVICE_PLUGIN_PATH=/tmp/var/lib/kubelet/device-plugins/
$ ./k8s-device-plugin
```

#### Check

```
$ kubectl get nodes
NAME        STATUS     ROLES    AGE     VERSION
fake-node   NotReady   <none>   5h6m    v0.0.0-master+$Format:%h$
master      Ready      <none>   6h14m   v1.17.0

$ kubectl describe nodes fake-node | grep gpu
nvidia.com/gpu:     8
```

## Run a Pod

```
# kubectl create -f nginx.yml
apiVersion: v1
kind: Pod
metadata:
  name: nginx
spec:
  containers:
    - name: nginx
    image: nginx
    imagePullPolicy: IfNotPresent
    resources:
      limits:
	nvidia.com/gpu: 10 # requesting 1 GPU
  nodeSelector:
    kubernetes.io/hostname: fake-node
```
