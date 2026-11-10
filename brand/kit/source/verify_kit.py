"""Verify the kit's structure, its manifest and the recorded manual PDF review.

Usage (or make brand-kit / make brand-kit-check):
  python verify_kit.py           verify, then record the result in reference/validation.json
  python verify_kit.py --check   verify, and fail unless reference/validation.json already records this result
"""
import json
import struct
import sys
import xml.etree.ElementTree as ET

from PIL import Image, ImageChops
from pypdf import PdfReader
from build_kit import ROOT, SOURCE, PRIMARY, DATE, sha, file_list
from tokens import INK, WHITE, BLACK, CYAN, USED


def rgb(hexvalue):
    return tuple(int(hexvalue[i:i+2],16) for i in (1,3,5))


def check_manifest():
    manifest=json.loads((ROOT/'manifest.json').read_text())
    files=manifest['files']
    assert set(files)=={p.relative_to(ROOT).as_posix() for p in file_list()},'manifest does not list exactly the kit files'
    for name,item in files.items():
        p=ROOT/name;assert sha(p)==item['sha256'] and p.stat().st_size==item['bytes'],name
    assert manifest['tokens']==dict(sorted(USED.items())),'the kit was built with other token values'
    return len(files)


def main():
    ns='{http://www.w3.org/2000/svg}'
    svgs=sorted((ROOT/'svg').glob('*.svg'));assert len(svgs)==19
    for p in svgs:
        doc=ET.parse(p).getroot()
        assert doc.tag==ns+'svg' and doc.find(ns+'title') is not None,p
        assert len(list(doc.iter(ns+'path')))>0,p
        assert all(e.tag not in {ns+'image',ns+'text',ns+'mask',ns+'script',ns+'style'} for e in doc.iter()),p
        for e in doc.iter():
            assert not any(k.endswith('href') for k in e.attrib),p
    pngs=sorted((ROOT/'png').glob('*.png'));assert len(pngs)==19
    for p in pngs:
        with Image.open(p) as im:
            im.load();assert im.mode=='RGBA',p
            a=im.getchannel('A');assert a.getextrema()==(0,255),p
            if 'app-icon' not in p.name:
                l,t,r,b=a.getbbox();assert l>0 and t>0 and r<im.width and b<im.height,p
                assert im.width==2400,p
            else:assert im.size==(2048,2048),p
    for label,colour in [('ink',INK),('white',WHITE),('black',BLACK),('cyan',CYAN)]:
        im=Image.open(ROOT/f'png/batlog-mark-{label}.png')
        assert {p[:3] for p in im.get_flattened_data() if p[3]==255}=={rgb(colour)},label
        assert im.getpixel((600,544))[3]==0,'History trace is not transparent'
    primary=Image.open(ROOT/'png/batlog-mark-ink.png');micro=Image.open(ROOT/'png/batlog-mark-micro-ink.png')
    assert primary.getpixel((600,624))[3]==255 and micro.getpixel((600,624))[3]==0
    icon_pngs=sorted((ROOT/'icons').rglob('*.png'));assert len(icon_pngs)==49
    for p in icon_pngs:
        with Image.open(p) as im:
            im.load();assert im.mode=='RGBA',p
            if p.name.startswith('batlog-symbol-'):
                width=int(p.stem.split('-')[-1]);assert im.width==width and im.height==round(width*2/3),p
            else:assert im.width==im.height,p
    for label in ['dark','light']:
        raw=(ROOT/f'icons/batlog-{label}.ico').read_bytes();reserved,kind,n=struct.unpack_from('<HHH',raw)
        assert reserved==0 and kind==1;sizes=set()
        for i in range(n):
            w,h,_,_,planes,bits,length,offset=struct.unpack_from('<BBBBHHII',raw,6+16*i)
            assert length>0 and offset+length<=len(raw);sizes.add((w or 256,h or 256))
        assert sizes=={(s,s) for s in [16,24,32,48,64,128,256]}
    with Image.open(ROOT/'icons/batlog.icns') as im:im.load();assert im.size==(1024,1024)
    pdf=ROOT/'batlog-brand-guide.pdf';reader=PdfReader(pdf);assert len(reader.pages)==7
    for page in reader.pages:assert len(page.extract_text())>150 and float(page.mediabox.width)==842
    review=json.loads((ROOT/'reference/pdf-review.json').read_text())
    assert review['sha256']==sha(pdf) and review['pages_reviewed']==list(range(1,8)),'PDF review is stale'
    # Fit is separate from curve quality: normalized silhouette overlap is not pixel identity.
    a=Image.open(SOURCE/'originals'/PRIMARY).convert('L').point(lambda p:255 if p<120 else 0)
    b=primary.getchannel('A').point(lambda p:255 if p>=128 else 0)
    a=a.crop(a.getbbox());b=b.crop(b.getbbox()).resize(a.size,Image.Resampling.NEAREST)
    intersection=sum(1 for p in ImageChops.darker(a,b).get_flattened_data() if p)
    union=sum(1 for p in ImageChops.lighter(a,b).get_flattened_data() if p)
    overlap=intersection/union;assert overlap>.95,overlap
    files=check_manifest()
    result={'date':DATE,'result':'pass','vector_svgs':19,'logo_pngs':19,'icon_pngs':49,
            'manifest_files':files,'normalized_source_silhouette_iou':overlap,
            'checks':['vector-only self-contained SVGs without masks','PNG alpha, bounds, dimensions and token colours',
                      'transparent channel and wider micro geometry','ICO resolution directories and ICNS decoding',
                      'seven PDF pages and current manual-review hash','manifest file hashes and token values'],
            'limits':['reconstruction, not pixel identity']}
    p=ROOT/'reference/validation.json';text=json.dumps(result,indent=2)+'\n'
    if '--check' in sys.argv[1:]:
        if not p.exists() or p.read_text()!=text:
            sys.exit('brand kit verify: reference/validation.json is stale; run make brand-kit')
    else:
        p.write_text(text)
    print(f'brand kit verify: ok, {files} manifest files, silhouette overlap {overlap:.4f}')


if __name__=='__main__':main()
