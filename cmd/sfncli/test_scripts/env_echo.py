#!/usr/bin/env python

import os, json, sys

out = "{}"
if len(sys.argv) > 1:
    i = json.loads(sys.argv[1])
    if i["key"] in os.environ:
        out = json.dumps({
            "result": os.environ[i["key"]]
        })
    else:
        out = json.dumps({"result": None})
print out
