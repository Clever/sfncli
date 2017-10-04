#!/usr/bin/env bash

# write something to stderr
echo $1 1>&2

# write stuff to stdout
echo $2

function on_sigterm {
    while true; do
        sleep 1
    done
}

trap on_sigterm SIGTERM

while true; do
    sleep 1
done
