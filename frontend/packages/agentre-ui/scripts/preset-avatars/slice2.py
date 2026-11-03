# 切 4x4 拼图 → 每张按贴纸实际边界裁紧，按角落底色补成正方形并统一留 8% 边，输出 128px webp。
import subprocess, sys, os
src, outdir, prefix = sys.argv[1], sys.argv[2], sys.argv[3]
os.makedirs(outdir, exist_ok=True)
flat = ['magick', src, '-background', 'white', '-flatten']
w, h = map(int, subprocess.check_output(['magick','identify','-format','%w %h',src]).split())
raw = subprocess.check_output(flat + ['-colorspace','gray','-depth','8','gray:-'])
def runs(is_white, n):
    segs=[]; start=None
    for i in range(n):
        if not is_white(i) and start is None: start=i
        if is_white(i) and start is not None: segs.append((start,i)); start=None
    if start is not None: segs.append((start,n))
    return [s for s in segs if s[1]-s[0] > n/8]
colw = lambda x: sum(raw[y*w+x] for y in range(0,h,4))/len(range(0,h,4)) > 240
roww = lambda y: sum(raw[y*w+x] for x in range(0,w,4))/len(range(0,w,4)) > 240
k=0
for (y0,y1) in runs(roww,h):
    for (x0,x1) in runs(colw,w):
        k+=1; inset=10; tw=x1-x0-2*inset; th=y1-y0-2*inset
        tile=f'/tmp/presetgen/_tile.png'
        subprocess.check_call(flat + ['-crop',f'{tw}x{th}+{x0+inset}+{y0+inset}','+repage',tile])
        bg = subprocess.check_output(['magick',tile,'-format','%[pixel:p{3,3}]','info:']).decode()
        bbox = subprocess.check_output(['magick',tile,'-fuzz','12%','-format','%@','info:']).decode()
        bw,bh = map(int, bbox.split('+')[0].split('x'))
        side = int(max(bw,bh)*1.16)
        out=f'{outdir}/{prefix}-{k:02d}.webp'
        subprocess.check_call(['magick',tile,'-fuzz','12%','-trim','+repage','-background',bg,'-gravity','center','-extent',f'{side}x{side}','-resize','128x128','-quality','88',out])
print(k,'tiles')
