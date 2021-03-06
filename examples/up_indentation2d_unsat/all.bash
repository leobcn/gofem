#!/bin/bash

FILES="a-coarse-elast-d2-q9 b-coarse-elast-d2-q9 \
       c-coarse-elast-d2-q9 d-coarse-elast-d2-q9"

for f in $FILES; do
    mpirun -np 4 gofem $f
    GenVtu $f
    go run doplot.go $f
    go run plotlrm.go $f
done
