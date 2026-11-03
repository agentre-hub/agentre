# 清理预设素材边缘：见 README.md「入库前清理」。依赖 Pillow + numpy。
import sys,glob,os,numpy as np
from PIL import Image
import numpy as np
from collections import deque
def comps(m):
    h,w=m.shape;lab=np.zeros((h,w),int);n=0;sizes=[]
    for y in range(h):
        for x in range(w):
            if m[y,x] and not lab[y,x]:
                n+=1;q=deque([(y,x)]);lab[y,x]=n;c=0
                while q:
                    a,b=q.popleft();c+=1
                    for da,db in((1,0),(-1,0),(0,1),(0,-1),(1,1),(-1,-1),(1,-1),(-1,1)):
                        u,v=a+da,b+db
                        if 0<=u<h and 0<=v<w and m[u,v] and not lab[u,v]:lab[u,v]=n;q.append((u,v))
                sizes.append(c)
    return lab,sizes
def dil(m,r):
    o=m.copy()
    h,w=m.shape
    for dy in range(-r,r+1):
        for dx in range(-r,r+1):
            if dy*dy+dx*dx>r*r:continue
            s=np.zeros_like(m)
            ys=slice(max(dy,0),h+min(dy,0));yd=slice(max(-dy,0),h+min(-dy,0))
            xs=slice(max(dx,0),w+min(dx,0));xd=slice(max(-dx,0),w+min(-dx,0))
            s[ys,xs]=m[yd,xd];o|=s
    return o
src,dst=sys.argv[1],sys.argv[2];os.makedirs(dst,exist_ok=True)
for f in sorted(glob.glob(src+'/*.webp')):
    n=os.path.basename(f)
    if 'brand' in n:continue
    im=np.array(Image.open(f).convert('RGB')).astype(int)
    bg=im[0,0];d=np.abs(im-bg).sum(2)
    lab,sz=comps(d>12)
    main=lab==(1+int(np.argmax(sz)))
    keep=dil(main,2)
    stray=(d>0)&~keep
    print(n,'stray px',int(stray.sum()),'of which strong',int((stray&(d>12)).sum()))
    im[stray]=bg
    Image.fromarray(im.astype('uint8')).save(f'{dst}/{n}',quality=90,method=6)
