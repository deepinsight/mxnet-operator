### Build docker image for mxnet from source, support distributed training

- Get the base image from ubuntu 16.04

- install cuda8.0 and cudnn libray into the base image ,get new docker image named such as:
```
   10.199.192.16/library/cuda:8.0-cudnn7-devel-ubuntu16.04
```
- get mxnet source code from git
```
  git clone https://github.com/apache/incubator-mxnet.git
```
- change to the target branch
```
   cd mxnet
   git checkout 0.12.0
```
- run docker build command to build the image:
  - build scprit in dockerfile.mx.gpu.dist will add USE_DIST_KVSTORE=1 for the distributed training
```
  docker build -f dockerfile.mx.gpu.dist .
```
