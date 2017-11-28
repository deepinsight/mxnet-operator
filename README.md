# K8s Custom Resource and Operator For MXNet jobs 
  (Forks initally from tf-operator project https://github.com/jlewi/mlkube.io.git now moved to      https://github.com/tensorflow/k8s)

## Requirements

Custom Resources require Kubernetes 1.7

## Motivation

Distributed MXNet training jobs require managing multiple sets of MXNet replicas. 
There are three kinds of replicas sets which act as different roles in the job:
Server (1-n instance)
Worker (1-n instance)
Scheduler(only one instance in a training cluster). 

K8s makes it easy to configure and deploy each set of MXNet replicas. Various tools like
 [helm](https://github.com/kubernetes/helm) and [ksonnet](http://ksonnet.heptio.com/) can
 be used to simplify generating the configs for a MXNet job. Helm will be supported a few days later.
 
 However, in addition to generating the configs we need some custom control logic because
 K8s built-in controllers (Jobs, ReplicaSets, StatefulSets, etc...) don't provide the semantics
 needed for managing MXNet jobs.
 
 To solve this we define a 
 [K8S Custom Resource](https://kubernetes.io/docs/concepts/api-extension/custom-resources/)
 and [Operator](https://coreos.com/blog/introducing-operators.html) to manage a MXNet
 job on K8s.

MxJob provides a K8s resource representing a single, distributed, mxnet training job. 
The Spec and Status (defined in [mx_job.go](pkg/spec/mx_job.go)
are customized for MXNet. The spec allows specifying the Docker image and arguments to use for each MXNet
replica (i.e. scheduler, worker, and parameter server). The status provides relevant information such as the number of
replicas in various states.

Using a CRD gives users the ability to create and manage mxnet Jobs just like builtin K8s resources. For example to
create a job

```
kubectl create -f examples/mx_job_local.yaml
```

To list jobs

```
kubectl get mxjobs

NAME               KINDS
example-dist-job   MxJob.v1beta1.mlkube.io
```

## Design

The code is closely modeled on Coreos's [etcd-operator](https://github.com/coreos/etcd-operator).

The MxJob Spec(defined in [mx_job.go](pkg/spec/mx_job.go)
reuses the existing Kubernetes structure PodTemplateSpec to describe MXNet processes. 
We use PodTemplateSpec because we want to make it easy for users to 
  configure the processes; for example setting resource requirements or adding volumes. 
  We expect
helm or ksonnet could be used to add syntactic sugar to create more convenient APIs for users not familiar
with Kubernetes.

Leader election allows a K8s deployment resource to be used to upgrade the operator.

## Installing the CRD and operator on your k8s cluster

1. Clone the repository


2. Deploy the operator

```
kubec create -f examples/mx_operator_deploy.yaml
```

3. Create distributed mxjob demo.

```
kubectl create -f examples/mx_job_dist.yaml
kubectl get job
NAME                                DESIRED   SUCCESSFUL   AGE
example-dist-job-scheduler-1kkf-0   1         1            1h
example-dist-job-server-1kkf-0      1         1            1h
example-dist-job-server-1kkf-1      1         1            1h
example-dist-job-worker-1kkf-0      1         1            1h
example-dist-job-worker-1kkf-1      1         1            1h
example-dist-job-worker-1kkf-2      1         1            1h

kubectl logs -f example-dist-job-worker-1kkf-2-hz22k
INFO:root:start with arguments Namespace(batch_size=128, benchmark=0, data_nthreads=4,     data_train='data/cifar10_train.rec', data_val='data/cifar10_val.rec', disp_batches=20, dtype='float32', gpus=None, image_shape='3,28,28', kv_store='dist_device_sync', load_epoch=None, lr=0.05, lr_factor=0.1, lr_step_epochs='200,250', max_random_aspect_ratio=0, max_random_h=36, max_random_l=50, max_random_rotate_angle=0, max_random_s=50, max_random_scale=1, max_random_shear_ratio=0, min_random_scale=1, model_prefix='./wltest', mom=0.9, monitor=0, network='resnet', num_classes=10, num_epochs=10, num_examples=50000, num_layers=2, optimizer='sgd', pad_size=4, random_crop=1, random_mirror=1, rgb_mean='123.68,116.779,103.939', test_io=0, top_k=0, wd=0.0001)
[02:52:36] src/io/iter_image_recordio_2.cc:153: ImageRecordIOParser2: data/cifar10_train.rec, use 1 threads for decoding..
[02:52:37] src/io/iter_image_recordio_2.cc:153: ImageRecordIOParser2: data/cifar10_val.rec, use 1 threads for decoding..
INFO:root:Epoch[0] Batch [20]	Speed: 84.39 samples/sec	accuracy=0.159970
INFO:root:Epoch[0] Batch [40]	Speed: 85.53 samples/sec	accuracy=0.250391
INFO:root:Epoch[0] Batch [60]	Speed: 90.39 samples/sec	accuracy=0.284375
INFO:root:Epoch[0] Batch [80]	Speed: 89.56 samples/sec	accuracy=0.294531
INFO:root:Epoch[0] Batch [100]	Speed: 91.03 samples/sec	accuracy=0.337500
INFO:root:Epoch[0] Batch [120]	Speed: 85.62 samples/sec	accuracy=0.342187
INFO:root:Epoch[0] Train-accuracy=0.363281
INFO:root:Epoch[0] Time cost=190.507
INFO:root:Saved checkpoint to "./wltest-2-0001.params"
 ```
<!--
## Using GPUs

The use of GPUs and K8s is still in flux. The following works with GKE & K8s 1.7.2. If this doesn't work on 
your setup please consider opening an issue.

### Prerequisites

We assume GPU device drivers have been installed on nodes on your cluster and resources have been defined for
GPUs.

Typically the NVIDIA drivers are installed on the host and mapped into containers because there are kernel and user
space drivers that need to be in sync. The kernel driver must be installed on the host and not in the container.

### Mounting NVIDIA libraries from the host.

The MxJob controller can be configured with a list of volumes that should be mounted from the host into the container
to make GPUs work. Here's an example:

```
accelerators:
  alpha.kubernetes.io/nvidia-gpu:
    volumes:
      - name: nvidia-libraries
        mountPath: /usr/local/nvidia/lib64 # This path is special; it is expected to be present in `/etc/ld.so.conf` inside the container image.
        hostPath: /home/kubernetes/bin/nvidia/lib
      - name: nvidia-debug-tools # optional
        mountPath: /usr/local/bin/nvidia
        hostPath: /home/kubernetes/bin/nvidia/bin
```

Here **alpha.kubernetes.io/nvidia-gpu** is the K8s resource name used for a GPU. The config above says that
any container which uses this resource should have the volumes mentioned mounted into the container
from the host.

The config is usually specified using a K8s ConfigMap and then passing the config into the controller via
the --controller_config_file. 

The helm package for the controller includes a config map suitable for GKE. This ConfigMap may need to be modified
for your cluster if you aren't using GKE.

### Using GPUs

To attach GPUs specify the GPU resource on the container e.g.

```
apiVersion: "mlkube.io/v1beta1"
kind: "MxJob"
metadata:
  name: "tf-smoke-gpu"
spec:
  replica_specs:
    - replicas: 1
      PsRootPort: 9091
      mxReplicaType: MASTER
      template:
        spec:
          containers:
            - image: gcr.io/tf-on-k8s-dogfood/tf_sample_gpu:latest
              name: tensorflow
              resources:
                limits:
                  alpha.kubernetes.io/nvidia-gpu: 1
          restartPolicy: OnFailure
```
-->
## Project Status

This is very much a prototype.


### Status information

The status information reported by the operator is hacky and not well thought out. In particular, we probably
need to figure out what the proper phases and conditions to report are.

### Failure/Termination Semantics

The semantics for aggregating status of individual replicas into overall MxJob status needs to be thought out.

### Dead/Unnecessary code

There is a lot of code from earlier versions (including the ETCD operator) that still needs to be cleaned up.

### Testing

There is minimal testing.

#### Unittests

There are some unittests.

#### E2E tests

The helm package provides some basic E2E tests.

## Building the Operator


Resolve dependencies (if you don't have glide install, check how to do it [here](https://github.com/Masterminds/glide/blob/master/README.md#install))

```
glide install
```


## Runing the Operator Locally

Running the operator locally (as opposed to deploying it on a K8s cluster) is convenient for debugging/development.

We can configure the operator to run locally using the configuration available in your kubeconfig to communicate with 
a K8s cluster.

Set your environment
```
export USE_KUBE_CONFIG=$(echo ~/.kube/config)
export MY_POD_NAMESPACE=default
export MY_POD_NAME=my-pod
```

    * MY_POD_NAMESPACE is used because the CRD is namespace scoped and we use the namespace of the controller to
      set the corresponding namespace for the resource.

TODO(jlewi): Do we still need to set MY_POD_NAME? Why?

## Go version

On ubuntu the default go package appears to be gccgo-go which has problems see [issue](https://github.com/golang/go/issues/15429) golang-go package is also really old so install from golang tarballs instead.

## Vendoring

You may need to remove the vendor directory of dependencies that also vendor dependencies as these may produce conflicts
with the versions vendored by mlkube; e.g.

```
rm -rf  vendor/k8s.io/apiextensions-apiserver/vendor
```
