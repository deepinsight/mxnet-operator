#!/bin/bash

REGISTRY="10.199.192.16/tensorflow"

# The image tag is based on the githash.
GITHASH=$(git rev-parse --short HEAD)
CHANGES=$(git diff-index --quiet HEAD -- || echo "untracked")
if [ -n "$CHANGES" ]; then
  # Get the hash of the diff.
  DIFFHASH=$(git diff  | shasum -a 256)
  DIFFHASH=${DIFFHASH:0:7}
  GITHASH=${GITHASH}-dirty-${DIFFHASH}
fi
IMAGE=${REGISTRY}/tf_operator:${GITHASH}

DIR="/Users/moon/docker/dockerfiles/tf-operator"
echo Use ${DIR} as context
GOOS=linux GOARCH=amd64 go install github.com/deepinsight/mlkube.io/cmd/tf_operator
GOOS=linux GOARCH=amd64 go install github.com/deepinsight/mlkube.io/test/e2e
cp ${GOPATH}/bin/linux_amd64/tf_operator ${DIR}/
cp ${GOPATH}/bin/linux_amd64/e2e ${DIR}/
cp Dockerfile ${DIR}/

docker build -t $IMAGE -f ${DIR}/Dockerfile ${DIR}
echo built $IMAGE
