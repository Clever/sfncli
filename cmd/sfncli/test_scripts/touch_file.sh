#!/usr/bin/env bash

mkdir -p $WORK_DIR
echo "{\"file\":\"$WORK_DIR/hello\"}" > $WORK_DIR/hello
cat $WORK_DIR/hello
