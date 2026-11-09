#!/usr/bin/env python3
"""Assert configured keep=1 after startup using only the supported catalog."""
import json,ssl,sys,time,urllib.request
base,ca,tokenfile=sys.argv[1:]
context=ssl.create_default_context(cafile=ca)
token=open(tokenfile).read().strip()
for _ in range(40):
    req=urllib.request.Request(base+'/admin/api/v1/backup-operations',headers={'Authorization':'Bearer '+token})
    with urllib.request.urlopen(req,context=context) as response:results=json.load(response)['results']
    if len(results)==1 and results[0]['kind']=='backup' and results[0]['state']=='complete':break
    time.sleep(1)
else:raise SystemExit('catalog did not expose exactly one complete configured result')
