#!/bin/bash
cd /mxnet
rm -rf build
find . -name *.o -delete
find . -name *.d -delete
find . -name *.cu -delete
rm -rf bin
rm -f lib/*.a
rm -rf deps
