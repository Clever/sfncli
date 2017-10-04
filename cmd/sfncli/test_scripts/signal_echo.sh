#!/usr/bin/env bash

exitCode=0
if [ "$#" -eq 2 ]; then
    exitCode=$1
fi

trap_with_arg() {
    func="$1" ; shift
    for sig ; do
        trap "$func $sig" "$sig"
    done
}

func_trap() {
    echo "{\"signal\": \"$1\"}"
    exit $exitCode
}

trap_with_arg func_trap 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60 61 62 63 64

while true; do
    sleep 1
done    
