#!/usr/bin/env bash

stderr=$1
stdout=$2
exitcode=$3
function on_sigterm {
    # write something to stderr
    echo $stderr 1>&2

    # write stuff to stdout
    echo $stdout

    # exit
    exit $exitcode
}

trap on_sigterm SIGTERM

while true; do
    sleep 1
done
