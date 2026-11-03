#!/bin/zsh
# $1 源文件  $2 输出名  $3 粉彩底色  $4 单色 logo 的填充色(可空)
src=$1; out=$2; bg=$3; fill=$4
magick "$src" -background none -trim +repage -resize 300x300 -gravity center -extent 340x340 _l.png
if [[ -n $fill ]]; then magick _l.png -fill "$fill" -colorize 100 _l.png; fi
magick _l.png -alpha extract -morphology Dilate Disk:9 -threshold 40% _o.png      # 深色描边形状
magick _o.png -morphology Dilate Disk:14 -threshold 40% _w.png                     # 白色贴纸边形状
magick -size 340x340 xc:'#ffffff' _w.png -alpha off -compose CopyOpacity -composite -colorspace sRGB -type TrueColorAlpha PNG32:_wl.png
magick -size 340x340 xc:'#26262b' _o.png -alpha off -compose CopyOpacity -composite -colorspace sRGB -type TrueColorAlpha PNG32:_ol.png
magick _wl.png _ol.png -compose over -composite _l.png -compose over -composite -background none -gravity center -extent 420x420 -colorspace sRGB PNG32:_s.png
magick -size 512x512 xc:"$bg" -colorspace sRGB \( _s.png -resize 384x384 \) -gravity center -compose over -composite -resize 128x128 -quality 90 "$out.webp"
