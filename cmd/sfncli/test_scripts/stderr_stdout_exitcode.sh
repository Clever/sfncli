#!/usr/bin/env bash

# write something to stderr
echo $1 1>&2

# write stuff to stdout
echo $2

# exit
exit $3
