#!/usr/bin/env bash

echo "{\"work_dir\":\"$WORK_DIR\"}" > $WORK_DIR/hello
cat $WORK_DIR/hello
