FROM 10.199.192.16/library/cuda:8.0-cudnn7-devel-ubuntu16.04
RUN rm -f /etc/apt/sources.list
COPY sources.list /etc/apt/
RUN apt-get update
RUN apt-get install -y libopencv-dev python-dev python-setuptools python-numpy python-pip 
RUN apt-get install -y libopenblas-dev liblapack-dev
RUN mkdir /mxnet
COPY mxnet /mxnet
RUN cd /mxnet; make -j48 USE_CUDA=1 USE_CUDA_PATH=/usr/local/cuda USE_CUDNN=1 USE_DIST_KVSTORE=0 USE_BLAS=openblas
RUN pip install --upgrade pip
RUN cd /mxnet/python; pip install -e .
COPY clean.sh ./
RUN chmod +x ./clean.sh
RUN ./clean.sh
