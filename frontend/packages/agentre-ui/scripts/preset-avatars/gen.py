import json, urllib.request, os, sys, base64, time
p=json.load(open(os.path.expanduser('~/.pi/agent/models.json')))['providers']['huu']
model, out, prompt = sys.argv[1], sys.argv[2], sys.argv[3]
size = sys.argv[4] if len(sys.argv)>4 else '1024x1024'
body={'model':model,'prompt':prompt,'size':size,'n':1}
req=urllib.request.Request(p['baseUrl']+'/images/generations', data=json.dumps(body).encode(),
  headers={'Authorization':'Bearer '+p['apiKey'],'Content-Type':'application/json'})
t=time.time()
try:
  r=json.load(urllib.request.urlopen(req, timeout=600))
except urllib.error.HTTPError as e:
  print('HTTP', e.code, e.read()[:800].decode(errors='ignore')); sys.exit(1)
d=r['data'][0]
if d.get('b64_json'): open(out,'wb').write(base64.b64decode(d['b64_json']))
elif d.get('url'): urllib.request.urlretrieve(d['url'], out)
print('ok', out, round(time.time()-t,1),'s', {k:v for k,v in r.items() if k!='data'})
