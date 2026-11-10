"""batlog brand kit builder: vectors, raster exports, review sheets, guide PDF and manifest.

Reads only the repository's own files: brand/tokens.json for every colour, geometry.py for the symbol, the fonts in
brand/kit/fonts and the supplied JPEGs in source/originals. Usage (or make brand-kit / make brand-kit-check):
  python build_kit.py           rebuild brand/kit in place
  python build_kit.py --check   rebuild into a temporary copy; fail unless every file comes out byte for byte the same
"""
from pathlib import Path
import sys
import tempfile
import hashlib
import json
import shutil
import subprocess

from PIL import Image, ImageDraw, ImageFont
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from reportlab.pdfgen import canvas
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont as RLFont
from reportlab.lib.colors import HexColor
from reportlab.lib.utils import ImageReader
from geometry import mark
from tokens import INK, CYAN, WHITE, BLACK, MIST, MUTED, SWATCH_EDGE, USED

SOURCE=Path(__file__).resolve().parent
ROOT=SOURCE.parent
TEMP=ROOT/'.build'
FONT=ROOT/'fonts/JetBrainsMono-SemiBold.ttf'
REGULAR=ROOT/'fonts/PlusJakartaSans-400.ttf'
SEMI=ROOT/'fonts/PlusJakartaSans-600.ttf'
DATE='2026-11-10' # adopted by batlog
PRIMARY='Designing_batlog_logo_symbol_343191ad_2K.jpg'
ALTERNATIVE='Design_logo_for_battery_tool_e655feea_2K.jpg'
jobs=[]


def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()


def write(name,text):
    p=ROOT/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text,encoding='utf-8');return p


def svg(body,w=1200,h=800,title='batlog'):
    return f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-label="{title}"><title>{title}</title>{body}</svg>'


def wordmark(color,size=170):
    font=TTFont(FONT);glyphs=font.getGlyphSet();cmap=font.getBestCmap();scale=size/font['head'].unitsPerEm
    x=0;parts=[]
    for char in 'batlog':
        name=cmap[ord(char)];pen=SVGPathPen(glyphs);glyphs[name].draw(pen)
        parts.append(f'<path d="{pen.getCommands()}" transform="translate({x:.5f} {size}) scale({scale:.8f} {-scale:.8f})"/>')
        x+=font['hmtx'][name][0]*scale
    return f'<g fill="{color}">{"".join(parts)}</g>',x


def export(name,body,w=1200,h=800,pixels=2400):
    p=write(f'svg/{name}.svg',svg(body,w,h,name))
    out=ROOT/f'png/{name}.png';out.parent.mkdir(exist_ok=True)
    jobs.append({'input':str(p),'output':str(out),'width':pixels})


def vectors():
    for label,color in [('ink',INK),('white',WHITE),('black',BLACK),('cyan',CYAN)]:
        export(f'batlog-mark-{label}',mark(color))
        letters,w=wordmark(color)
        export(f'batlog-horizontal-{label}',f'<g transform="translate(0 16) scale(.36)">{mark(color)}</g><g transform="translate(465 45)">{letters}</g>',int(w+500),330)
    for label,color in [('ink',INK),('white',WHITE),('black',BLACK)]:
        export(f'batlog-mark-micro-{label}',mark(color,True))
        letters,w=wordmark(color)
        export(f'batlog-wordmark-{label}',f'<g transform="translate(25 10)">{letters}</g>',int(w+50),245)
        letters,w=wordmark(color,170)
        export(f'batlog-stacked-{label}',f'{mark(color)}<g transform="translate({(1200-w)/2:g} 800)">{letters}</g>',1200,1060)
    for label,bg,fg in [('dark',INK,WHITE),('light',WHITE,INK)]:
        body=f'<rect width="1000" height="1000" rx="210" fill="{bg}"/><g transform="translate(50 200) scale(.75)">{mark(fg,True)}</g>'
        export(f'batlog-app-icon-{label}',body,1000,1000,2048)


def render():
    TEMP.mkdir(exist_ok=True);p=TEMP/'render-jobs.json';p.write_text(json.dumps(jobs))
    subprocess.run(['node',str(SOURCE/'render.mjs'),str(p)],check=True)


def icons():
    folder=ROOT/'icons';folder.mkdir(exist_ok=True)
    for label in ['dark','light']:
        im=Image.open(ROOT/f'png/batlog-app-icon-{label}.png')
        for size in [16,24,32,48,64,128,180,192,256,512,1024]:
            im.resize((size,size),Image.Resampling.LANCZOS).save(folder/f'batlog-app-{label}-{size}.png')
        im.save(folder/f'batlog-{label}.ico',sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
    # macOS iconset is a proposal for publishing; batlog itself is a CLI/TUI.
    iconset=folder/'batlog.iconset';iconset.mkdir(exist_ok=True)
    for base in [16,32,128,256,512]:
        for scale in [1,2]:
            size=base*scale
            Image.open(ROOT/'png/batlog-app-icon-dark.png').resize((size,size),Image.Resampling.LANCZOS).save(iconset/f'icon_{base}x{base}{"@2x" if scale==2 else ""}.png')
    Image.open(ROOT/'png/batlog-app-icon-dark.png').resize((1024,1024),Image.Resampling.LANCZOS).save(folder/'batlog.icns',format='ICNS')
    shutil.copy2(folder/'batlog-app-dark-32.png',folder/'favicon-32.png')
    shutil.copy2(folder/'batlog-app-dark-180.png',folder/'apple-touch-icon.png')
    write('icons/site.webmanifest',json.dumps({'name':'batlog','short_name':'batlog','icons':[{'src':f'batlog-app-dark-{s}.png','sizes':f'{s}x{s}','type':'image/png','purpose':'any'} for s in [192,512]],'theme_color':INK,'background_color':WHITE},indent=2)+'\n')
    icon_jobs=[]
    # Tight rectangular icons use the same geometry, avoiding excessive square padding.
    for label,color in [('ink',INK),('white',WHITE),('black',BLACK)]:
        for size in [16,24,32,48,64]:
            icon_jobs.append({'input':str(ROOT/f'svg/batlog-mark-micro-{label}.svg'),
                              'output':str(folder/f'batlog-symbol-{label}-{size}.png'),'width':size})
    p=TEMP/'icon-jobs.json';p.write_text(json.dumps(icon_jobs))
    subprocess.run(['node',str(SOURCE/'render.mjs'),str(p)],check=True)


def paste(out,im,box,bg=WHITE):
    x,y,w,h=box;layer=Image.new('RGBA',(w,h),bg);im=im.convert('RGBA');im.thumbnail((w,h),Image.Resampling.LANCZOS)
    layer.alpha_composite(im,((w-im.width)//2,(h-im.height)//2));out.paste(layer.convert('RGB'),(x,y))


def crop_source(p):
    im=Image.open(p).convert('RGB');mask=im.convert('L').point(lambda x:255 if x<120 else 0)
    # Disregard the thin frame in the first supplied image.
    d=ImageDraw.Draw(mask);d.rectangle((0,0,im.width-1,im.height-1),outline=0,width=24)
    return im.crop(mask.getbbox())


def sheets():
    regular=lambda s:ImageFont.truetype(str(REGULAR),s)
    semi=lambda s:ImageFont.truetype(str(SEMI),s)
    def txt(d,xy,s,size=23,fill=INK):d.text(xy,s,font=regular(size),fill=fill)
    overview=Image.new('RGB',(1800,1250),MIST);d=ImageDraw.Draw(overview)
    d.text((70,45),'batlog',font=ImageFont.truetype(str(FONT),70),fill=INK)
    txt(d,(72,143),'Understand what drained your battery.',29)
    txt(d,(72,197),'BRAND KIT / RECONSTRUCTED SYMBOL / ADOPTED / '+DATE,18,MUTED)
    d.rounded_rectangle((70,265,1110,850),radius=24,fill=WHITE)
    paste(overview,Image.open(ROOT/'png/batlog-mark-ink.png'),(110,305,960,480))
    txt(d,(105,795),'PRIMARY SYMBOL / battery + recorded history',20)
    d.rounded_rectangle((1140,265,1730,850),radius=24,fill=INK)
    paste(overview,Image.open(ROOT/'png/batlog-mark-white.png'),(1180,350,510,340),INK)
    txt(d,(1180,785),'REVERSE / true transparent cutout',20,WHITE)
    d.rounded_rectangle((70,880,1730,1180),radius=24,fill=WHITE)
    paste(overview,Image.open(ROOT/'png/batlog-horizontal-ink.png'),(100,900,1000,240))
    for i,col in enumerate([INK,CYAN,WHITE]):
        x=1310+i*125;d.rounded_rectangle((x,940,x+95,1035),radius=15,fill=col,outline=SWATCH_EDGE);txt(d,(x,1060),col,16)
    overview.save(ROOT/'batlog-overview.png')
    comp=Image.new('RGB',(1800,780),WHITE);d=ImageDraw.Draw(comp)
    panels=[('Primary source / selected',crop_source(SOURCE/'originals'/PRIMARY)),
            ('Reconstructed primary',Image.open(ROOT/'png/batlog-mark-ink.png').crop(Image.open(ROOT/'png/batlog-mark-ink.png').getchannel('A').getbbox())),
            ('Alternative source / retained',crop_source(SOURCE/'originals'/ALTERNATIVE))]
    for i,(label,im) in enumerate(panels):
        paste(comp,im,(i*600+25,100,550,440));txt(d,(i*600+25,590),label,21)
    txt(d,(25,680),'Primary: one clear discharge trace. Alternative: nested shapes add detail and compete at small sizes.',22,MUTED)
    comp.save(ROOT/'reference/reconstruction-comparison.png')
    small=Image.new('RGB',(1700,900),MIST);d=ImageDraw.Draw(small)
    txt(d,(35,25),'MICRO SYMBOL / actual size above, nearest-neighbor enlargement below',22)
    for row,label in enumerate(['ink','white']):
        y=80+row*380;bg=WHITE if label=='ink' else INK
        d.rounded_rectangle((30,y,1670,y+345),radius=18,fill=bg)
        for i,size in enumerate([16,24,32,48,64]):
            x=65+i*320;im=Image.open(ROOT/f'icons/batlog-symbol-{label}-{size}.png')
            paste(small,im,(x,y+28,size,round(size*2/3)),bg)
            enlarged=im.resize((240,round(im.height*240/im.width)),Image.Resampling.NEAREST)
            paste(small,enlarged,(x,y+105,240,160),bg)
            txt(d,(x,y+295),f'{size}px width',18,INK if label=='ink' else WHITE)
    txt(d,(35,855),'Micro widens the transparent history line. Check final rendering at the intended size and background.',21)
    small.save(ROOT/'reference/small-size-check.png')
    detail=Image.new('RGB',(1800,850),WHITE);d=ImageDraw.Draw(detail)
    txt(d,(30,35),'GEOMETRY REVIEW / exact arcs and straight segments',30)
    im=Image.open(ROOT/'png/batlog-mark-ink.png')
    for i,(label,box) in enumerate([('Battery corner',(110,180,460,570)),('History turn',(775,430,1050,900)),('Integrated terminal',(1920,460,2290,1080))]):
        paste(detail,im.crop(box),(i*600+30,130,540,500));txt(d,(i*600+30,680),label,25)
    txt(d,(30,775),'One compound vector path with an even-odd cutout. No tracing, image embedding, masks or external fonts.',21,MUTED)
    detail.save(ROOT/'reference/curve-detail-check.png')


def guide():
    for name,p in [('Body',REGULAR),('Semi',SEMI),('Mono',FONT)]:pdfmetrics.registerFont(RLFont(name,str(p)))
    c=canvas.Canvas(str(ROOT/'batlog-brand-guide.pdf'),pagesize=(842,595),invariant=1)
    c.setTitle('batlog - Brand guide');c.setAuthor('batlog')
    def rect(x,y,w,h,col,r=0):c.setFillColor(HexColor(col));c.roundRect(x,y,w,h,r,stroke=0,fill=1)
    def txt(x,y,s,size=12,color=INK,font='Body'):
        c.setFillColor(HexColor(color));c.setFont(font,size);c.drawString(x,y,s)
    def para(x,y,s,width=350,size=12,color=MUTED,leading=19):
        line=''
        for word in s.split():
            candidate=f'{line} {word}'.strip()
            if pdfmetrics.stringWidth(candidate,'Body',size)>width:txt(x,y,line,size,color);y-=leading;line=word
            else:line=candidate
        if line:txt(x,y,line,size,color);y-=leading
        return y
    def img(name,x,y,w,h):c.drawImage(ImageReader(str(ROOT/name)),x,y,width=w,height=h,mask='auto',preserveAspectRatio=True,anchor='c')
    def page(n,title,subtitle,kicker):
        rect(0,0,842,595,WHITE);txt(42,556,'BATLOG / BRAND GUIDE',10,MUTED,'Semi');txt(680,556,DATE,10,MUTED)
        txt(42,510,title,30,INK,'Semi');txt(42,482,subtitle,11,MUTED)
        txt(42,25,kicker,9,MUTED);txt(760,25,f'{n:02d} / 07',9,MUTED)
    page(1,'Understand what drained your battery.','A local-first macOS tool for battery history and per-app energy.','IDENTITY')
    txt(42,410,'batlog',60,INK,'Mono')
    para(44,358,'Quiet recording. Clear history. Useful answers about the apps and sessions that used your battery.',325,16,INK,25)
    rect(435,200,365,245,MIST,22);img('png/batlog-mark-ink.png',450,220,335,200)
    para(44,214,'The battery identifies the subject. The descending trace represents measurements over time. The simple silhouette works in one color and on light or dark backgrounds.',335,12)
    para(44,106,'Owner-supplied Flow concept; hand-authored vector reconstruction. The lettering and usage rules are adopted. Signal Cyan stays an optional accent, and the headline is suggested brand copy.',730,10)
    c.showPage()
    page(2,'Choose the clearest story.','The solid battery with one descending line is the selected primary.','SOURCE & RECONSTRUCTION')
    img('reference/reconstruction-comparison.png',30,175,782,285)
    para(42,152,'The alternative is preserved, not discarded. Its nested frame and trace introduce more visual competition; the primary communicates the same idea with fewer shapes.',745,12)
    para(42,88,'The SVG regularizes the silhouette, corner radii and step spacing. It is reconstructed artwork, not an original Flow vector or a pixel-identical copy. Both supplied JPEGs remain unchanged.',745,10)
    c.showPage()
    page(3,'A compact, practical logo system.','Primary mark, small-size variant, wordmark and lockups.','LOGO SYSTEM')
    for i,(label,name,desc) in enumerate([('Primary','batlog-mark-ink','For normal placements; start at 48px width.'),('Micro','batlog-mark-micro-ink','Wider history cutout for 16-32px symbols.'),('Reverse','batlog-mark-white','White artwork with transparent negative space.')]):
        x=42+i*260;bg=INK if i==2 else MIST;rect(x,290,240,160,bg,12);img(f'png/{name}.png',x+10,300,220,140)
        txt(x,263,label,17,INK,'Semi');para(x,238,desc,235,11)
    img('png/batlog-horizontal-ink.png',42,105,370,105)
    para(445,172,'Lowercase batlog is the written identity and command name. The lettering is JetBrains Mono SemiBold, delivered as vector outlines.',350,12)
    para(445,94,'Use the full name in introductions: batlog, a macOS battery and per-app energy tracker.',350,11)
    c.showPage()
    page(4,'Neutral ink. A measured accent.','The source is monochrome; cyan is an optional brand accent.','COLOR & TYPOGRAPHY')
    for i,(label,col,role) in enumerate([('Battery Ink',INK,'Source median normalized to solid ink'),('Signal Cyan',CYAN,'Optional digital accent'),('White',WHITE,'Background / reverse artwork')]):
        x=42+i*260;rect(x,310,240,140,MIST,12);rect(x+12,322,216,116,col,8)
        txt(x,282,label,17,INK,'Semi');txt(x,258,col,14,MUTED);txt(x,232,role,10,MUTED)
    txt(42,176,'batlog',34,INK,'Mono');para(42,131,'JetBrains Mono SemiBold for the wordmark and command examples. Plus Jakarta Sans for this guide and optional supporting brand copy. Both font families include OFL licenses.',350,11)
    txt(445,176,'Terminal colors stay flexible.',17,INK,'Semi')
    para(445,145,'The app uses ANSI cyan, green, yellow and red. Their actual colors depend on the terminal theme. Signal Cyan is never a terminal color; the ANSI roles are tokens too.',350,11)
    c.showPage()
    page(5,'Keep the history line clear.','Rules for placed logos; check them at the final size.','SPACING & CONTRAST')
    rect(42,190,350,250,MIST,14);img('png/batlog-mark-ink.png',87,230,260,173.3333)
    bbox=Image.open(ROOT/'png/batlog-mark-ink.png').getchannel('A').getbbox();scale=260/2400
    l,t,r,b=[v*scale for v in bbox];pad=(b-t)/4
    c.setStrokeColor(HexColor(CYAN));c.setDash(3,3);c.rect(87+l-pad,230+173.3333-b-pad,r-l+2*pad,b-t+2*pad,stroke=1,fill=0);c.setDash()
    txt(69,205,'Clear space: 1/4 of visible battery height',10,MUTED)
    y=426
    for title,body in [('Choose contrast','Ink or black on light surfaces; white on dark. Preserve the transparent history line.'),('Choose the right file','Use the primary at 48px or above, micro at 16-32px, and horizontal lockups at 180px or above.'),('Keep the meaning intact','Scale uniformly. Keep the battery terminal on the right and the history descending left to right. Avoid shadows, extra chart details or distorted steps.')]:
        txt(435,y,title,15,INK,'Semi');y=para(435,y-25,body,355,11)-19
    para(42,126,'Clear space sits outside the visible shape; the export canvas alone may not supply it. At 16px use the micro file and inspect the actual background. The icon is an identity mark, not a live battery indicator.',750,11)
    c.showPage()
    page(6,'Ready for docs and sharing.','Platform assets are candidates for publishing; batlog remains a CLI/TUI.','ICONS & EXPORTS')
    img('png/batlog-app-icon-dark.png',42,250,180,180);img('png/batlog-app-icon-light.png',238,250,180,180)
    txt(450,420,'Dark and light tiles',18,INK,'Semi')
    para(450,391,'Square PNGs from 16 to 1024px, Windows ICO exports, macOS ICNS and iconset, favicon and Apple touch icon are included for brand reuse. They do not add Windows support, a native Mac app or a browser UI.',340,12)
    para(450,272,'Transparent rectangular micro symbols are supplied separately. Use these for tight spaces; use square tiles for profiles and distribution pages.',340,11)
    for row,label in enumerate(['ink','white']):
        y=146-row*70;bg=MIST if label=='ink' else INK;rect(42,y-12,758,65,bg,8)
        for i,size in enumerate([16,24,32,48,64]):
            x=65+i*145;img(f'icons/batlog-symbol-{label}-{size}.png',x,y,48,32)
            txt(x+58,y+11,f'{size}px',10,INK if label=='ink' else WHITE)
    c.showPage()
    page(7,'Clear claims. Useful answers.','Voice and product facts that should stay consistent across brand materials.','PRODUCT & HANDOFF')
    txt(42,432,'What batlog does',19,INK,'Semi')
    y=para(42,400,'Records battery and per-app energy on your Mac. Helps explain which apps drained a charge, how long sessions lasted, and how battery health changes over time.',350,12)
    y=para(42,y-16,'For terminal-comfortable MacBook users, especially developers investigating battery drain. Local-only data; background recording without sudo.',350,12)
    para(42,y-16,'It measures and reports. It does not limit charging, throttle apps, or promise to extend battery life.',350,12)
    txt(445,432,'How to talk about it',19,INK,'Semi')
    y=para(445,400,'Be direct, calm and specific. Say "See which apps used the most energy" or "Compare battery sessions over time." Distinguish estimated battery cost from direct per-app measurements.',350,12)
    y=para(445,y-16,'Avoid unsupported claims such as "double your battery life," "exact battery cost for every app," or "automatic battery optimization."',350,12)
    txt(445,y-15,'batlog top --session last',14,INK,'Mono')
    para(42,99,'In the batlog repository: brand/kit holds these SVGs, PNGs, icons, fonts, the builder and this guide; brand/tokens.json is the palette; docs/reference/brand.md has the rules. The README shows the lockup; the terminal keeps ANSI colors and the user\'s font.',745,10)
    c.showPage();c.save()


# Hand-written files in brand/kit that the builder neither reads nor writes; everything else is an input or an
# output and is hashed in manifest.json. brand/check (make brand-check) reads the same list.
UNMANAGED=['README.md','manifest.json','history/','reference/pdf-review.json','reference/validation.json']
IGNORED={'.build','node_modules','.venv','__pycache__','.DS_Store'}


def managed(p,root=ROOT):
    rel=p.relative_to(root).as_posix()
    return not any(part in IGNORED for part in p.relative_to(root).parts) and not any(
        rel==u or (u.endswith('/') and rel.startswith(u)) for u in UNMANAGED)


def file_list(root=ROOT):
    return sorted(p for p in root.rglob('*') if p.is_file() and managed(p,root))


def package():
    files={p.relative_to(ROOT).as_posix():{'sha256':sha(p),'bytes':p.stat().st_size} for p in file_list()}
    write('manifest.json',json.dumps({'about':'Every input and output of the kit builder, and the token values it '
        'drew with. brand/check fails when a file or a token no longer matches: run make brand-kit.',
        'brand':'batlog','date':DATE,'tokens':dict(sorted(USED.items())),'files':files},indent=2)+'\n')


OUTPUTS=['svg','png','icons','reference/curve-detail-check.png','reference/reconstruction-comparison.png',
         'reference/small-size-check.png','batlog-overview.png','batlog-brand-guide.pdf','manifest.json']


def check():
    """Rebuild in a temporary copy of brand/ and compare every file with the committed kit."""
    with tempfile.TemporaryDirectory() as tmp:
        brand=Path(tmp)/'brand';kit=brand/'kit'
        brand.mkdir();shutil.copy2(ROOT.parent/'tokens.json',brand/'tokens.json')
        shutil.copytree(ROOT,kit,ignore=shutil.ignore_patterns(*IGNORED))
        for out in OUTPUTS:
            p=kit/out
            shutil.rmtree(p) if p.is_dir() else p.unlink()
        (kit/'source/node_modules').symlink_to(SOURCE/'node_modules')
        subprocess.run([sys.executable,str(kit/'source/build_kit.py')],check=True,stdout=subprocess.DEVNULL)
        want={p.relative_to(ROOT).as_posix() for p in file_list()}|{'manifest.json'}
        got={p.relative_to(kit).as_posix() for p in file_list(kit)}|{'manifest.json'}
        bad=sorted(f'missing from the rebuild: {n}' for n in want-got)+sorted(f'not committed: {n}' for n in got-want)
        bad+=sorted(f'differs: {n}' for n in want&got if (ROOT/n).read_bytes()!=(kit/n).read_bytes())
        for b in bad:print('brand kit check:',b,file=sys.stderr)
        if bad:sys.exit(f'brand kit check: {len(bad)} file(s) differ from a fresh build; run make brand-kit')
        print(f'brand kit check: ok, {len(want)} files rebuilt byte for byte')


def main():
    if '--check' in sys.argv[1:]:
        return check()
    vectors();render();icons();sheets();guide();package()
    print(f'Built {len(jobs)} batlog SVG/PNG variants, icon exports, review sheets and a seven-page PDF.')


if __name__=='__main__':main()
