#!/bin/bash
set -e

SRC_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ROOT_DIR=${SRC_DIR}/../../

. ${ROOT_DIR}/config.sh

# The image tag is based on the githash.
GITHASH=$(git rev-parse --short HEAD)
CHANGES=$(git diff-index --quiet HEAD -- || echo "untracked")
if [ -n "$CHANGES" ]; then
  # Get the hash of the diff.
  DIFFHASH=$(git diff  | sha256sum)
  DIFFHASH=${DIFFHASH:0:7}
  GITHASH=${GITHASH}-dirty-${DIFFHASH}
fi

DIR=`mktemp -d`
echo Use ${DIR} as context
go install github.com/deepinsight/mxnet-operator/cmd/mx_operator
go install github.com/deepinsight/mxnet-operator/test/e2e
cp ${GOPATH}/bin/mx_operator ${DIR}/
cp ${GOPATH}/bin/e2e ${DIR}/
cp ${SRC_DIR}/Dockerfile ${DIR}/
REGISTRY=10.199.192.16
IMAGE=${REGISTRY}/tensorflow/mx_operator:1.7-${GITHASH}
docker build --build-arg http_proxy=http://172.16.22.7:8118 -t $IMAGE -f ${DIR}/Dockerfile ${DIR}
docker push $IMAGE
echo pushed $IMAGE
