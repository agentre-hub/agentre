#!/bin/sh
# $1 源文件  $2 输出名  $3 粉彩底色  $4 单色 logo 的填充色(可空或 -)
set -eu
src=$1; out=$2; bg=$3; fill=${4:-}
# 中间产物归本次调用所有，不覆盖当前目录文件，也不与并行任务串图。
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
magick "$src" -background none -trim +repage -resize 300x300 -gravity center -extent 340x340 "$scratch/l.png"
if [ -n "$fill" ] && [ "$fill" != "-" ]; then magick "$scratch/l.png" -fill "$fill" -colorize 100 "$scratch/l.png"; fi
magick "$scratch/l.png" -alpha extract -morphology Dilate Disk:9 -threshold 40% "$scratch/o.png" # 深色描边形状
magick "$scratch/o.png" -morphology Dilate Disk:14 -threshold 40% "$scratch/w.png" # 白色贴纸边形状
magick -size 340x340 xc:'#ffffff' "$scratch/w.png" -alpha off -compose CopyOpacity -composite -colorspace sRGB -type TrueColorAlpha "PNG32:$scratch/wl.png"
magick -size 340x340 xc:'#26262b' "$scratch/o.png" -alpha off -compose CopyOpacity -composite -colorspace sRGB -type TrueColorAlpha "PNG32:$scratch/ol.png"
magick "$scratch/wl.png" "$scratch/ol.png" -compose over -composite "$scratch/l.png" -compose over -composite -background none -gravity center -extent 420x420 -colorspace sRGB "PNG32:$scratch/s.png"
magick -size 512x512 xc:"$bg" -colorspace sRGB \( "$scratch/s.png" -resize 384x384 \) -gravity center -compose over -composite -resize 128x128 -quality 90 "$out.webp"
