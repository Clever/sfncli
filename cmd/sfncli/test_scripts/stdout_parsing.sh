#!/usr/bin/env bash

echo "some"
echo "other"
echo "stuff"
echo "even some stderr" 2>&1
echo "and now for the main event:"
echo "{\"task\": \"output\"}"
