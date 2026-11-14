"""Draws MySpotify's icons. Run on Windows, with Pillow: python tools/icons.py

Every icon is rendered from clean vector geometry:

  resources/icon.svg, icon.png   the app's icon, 1024 px (Linux, macOS's .icns)
  resources/icon-small.svg       bold simplified variant for 16-24 px
  resources/icon.ico             Windows: icon-small for 16-24 px, icon above
  internal/app/tray.png          the notification area's and the menu bar's,
                                 a template: black and clear, which Windows draws
                                 white on a dark taskbar and macOS tints itself
  AppIcon.icon                   the Dock's, an Icon Composer document whose tile
                                 is light or dark as macOS is (mygo.json macos.icon)
  docs/icon.png                  the README's and website's, 256 px

Edge renders the SVGs. Then rsrc_windows_amd64.syso again, as DEVELOPMENT.md says.
"""
import json
import os
import shutil
import subprocess
import tempfile

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EDGE = 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
C = 512


def note_emblem(fill, small=False):
    """Sculpted beamed musical note: bold & legible at every size."""
    if small:
        return (
            f'<g fill="{fill}" transform="translate(18 14)">'
            '<ellipse cx="332" cy="692" rx="134" ry="102" transform="rotate(-18 332 692)"/>'
            '<ellipse cx="660" cy="624" rx="134" ry="102" transform="rotate(-18 660 624)"/>'
            '<path d="M378 686 L378 308 Q378 264 422 256 L706 202 Q748 194 748 238 '
            'L748 618 L664 618 L664 338 L462 376 L462 686 Z"/>'
            '</g>'
        )
    return (
        f'<g fill="{fill}" transform="translate(16 14)">'
        '<ellipse cx="352" cy="672" rx="112" ry="86" transform="rotate(-18 352 672)"/>'
        '<ellipse cx="642" cy="614" rx="112" ry="86" transform="rotate(-18 642 614)"/>'
        '<path d="M394 668 L394 318 Q394 276 436 268 L688 220 Q726 213 726 254 '
        'L726 610 L656 610 L656 346 L464 382 L464 668 Z"/>'
        '</g>'
    )


def app_tile_defs():
    return (
        '<linearGradient id="tileBg" x1="12%" y1="6%" x2="88%" y2="96%">'
        '<stop offset="0%" stop-color="#142d22"/>'
        '<stop offset="52%" stop-color="#0b1712"/>'
        '<stop offset="100%" stop-color="#050908"/>'
        '</linearGradient>'
        '<radialGradient id="tileGlow" cx="50%" cy="44%" r="54%">'
        '<stop offset="0%" stop-color="#1ed760" stop-opacity="0.28"/>'
        '<stop offset="55%" stop-color="#00c7b7" stop-opacity="0.10"/>'
        '<stop offset="100%" stop-color="#000000" stop-opacity="0"/>'
        '</radialGradient>'
        '<linearGradient id="tileRim" x1="0%" y1="0%" x2="100%" y2="100%">'
        '<stop offset="0%" stop-color="#ffffff" stop-opacity="0.24"/>'
        '<stop offset="48%" stop-color="#1ed760" stop-opacity="0.20"/>'
        '<stop offset="100%" stop-color="#00d2c8" stop-opacity="0.16"/>'
        '</linearGradient>'
        '<linearGradient id="noteGrad" x1="16%" y1="88%" x2="86%" y2="10%">'
        '<stop offset="0%" stop-color="#1ed760"/>'
        '<stop offset="58%" stop-color="#29ef8e"/>'
        '<stop offset="100%" stop-color="#00d4e6"/>'
        '</linearGradient>'
        '<filter id="noteShadow" x="-16%" y="-16%" width="132%" height="136%">'
        '<feDropShadow dx="0" dy="14" stdDeviation="20" flood-color="#000000" flood-opacity="0.45"/>'
        '</filter>'
    )


def app_icon(small=False):
    if small:
        body = (
            '<rect x="44" y="44" width="936" height="936" rx="236" fill="url(#tileBg)"/>'
            '<rect x="44" y="44" width="936" height="936" rx="236" fill="url(#tileGlow)"/>'
            '<rect x="52" y="52" width="920" height="920" rx="228" fill="none" stroke="url(#tileRim)" stroke-width="18"/>'
            + note_emblem('url(#noteGrad)', small=True)
        )
    else:
        body = (
            '<rect x="64" y="64" width="896" height="896" rx="220" fill="url(#tileBg)"/>'
            '<rect x="64" y="64" width="896" height="896" rx="220" fill="url(#tileGlow)"/>'
            '<rect x="70" y="70" width="884" height="884" rx="214" fill="none" stroke="url(#tileRim)" stroke-width="10"/>'
            f'<g filter="url(#noteShadow)">{note_emblem("url(#noteGrad)", small=False)}</g>'
            '<path d="M236 168 Q512 138 788 168" fill="none" stroke="#ffffff" stroke-opacity="0.08" stroke-width="6" stroke-linecap="round"/>'
        )
    return svg(body, app_tile_defs())


def svg(body, defs=''):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">'
            f'<defs>{defs}</defs>{body}</svg>\n')


def write(path, text):
    with open(os.path.join(ROOT, path), 'w', encoding='utf-8', newline='\n') as f:
        f.write(text)


def render(svg_text, work):
    """svg_text as a 1024 px image, on nothing."""
    src, out = os.path.join(work, 'in.svg'), os.path.join(work, 'out.png')
    with open(src, 'w', encoding='utf-8') as f:
        f.write(svg_text)
    subprocess.run([EDGE, '--headless=new', '--disable-gpu', '--hide-scrollbars', '--user-data-dir=' + os.path.join(work, 'edge'),
                    '--default-background-color=00000000', '--window-size=1024,1024', '--screenshot=' + out,
                    'file:///' + src.replace(os.sep, '/')], check=True, capture_output=True)
    with Image.open(out) as im:
        return im.convert('RGBA')


def sized(im, px):
    return im.resize((px, px), Image.LANCZOS)


def main():
    big, small = app_icon(small=False), app_icon(small=True)
    # Monochrome tray template: bold note silhouette scaled comfortably into the 1024 canvas.
    tray = svg(
        f'<g transform="translate({C} {C}) scale(1.18) translate(-{C} -{C})">'
        f'{note_emblem("#000000", small=True)}</g>'
    )
    # macOS Icon Composer document supplies its own light/dark tile, so keep only the emblem.
    dock_defs = (
        '<linearGradient id="noteGrad" x1="16%" y1="88%" x2="86%" y2="10%">'
        '<stop offset="0%" stop-color="#1ed760"/>'
        '<stop offset="58%" stop-color="#29ef8e"/>'
        '<stop offset="100%" stop-color="#00d4e6"/>'
        '</linearGradient>'
    )
    dock = svg(
        f'<g transform="translate({C} {C}) scale(1.06) translate(-{C} -{C})">'
        f'{note_emblem("url(#noteGrad)", small=False)}</g>',
        dock_defs,
    )
    write('resources/icon.svg', big)
    write('resources/icon-small.svg', small)

    work = tempfile.mkdtemp()
    try:
        big_im, small_im, tray_im, dock_im = (render(s, work) for s in (big, small, tray, dock))
    finally:
        shutil.rmtree(work, ignore_errors=True)

    big_im.save(os.path.join(ROOT, 'resources/icon.png'))
    sized(big_im, 256).save(os.path.join(ROOT, 'docs/icon.png'))
    frames = [sized(small_im, px) for px in (16, 20, 24)] + [sized(big_im, px) for px in (32, 48, 64, 128, 256)]
    frames[-1].save(os.path.join(ROOT, 'resources/icon.ico'), sizes=[f.size for f in frames], append_images=frames[:-1])

    # A template is black where it shows: only its alpha says where.
    t = sized(tray_im, 32)
    black = Image.new('L', t.size, 0)
    Image.merge('RGBA', (black, black, black, t.getchannel('A'))).save(os.path.join(ROOT, 'internal/app/tray.png'))

    icon = os.path.join(ROOT, 'AppIcon.icon')
    os.makedirs(os.path.join(icon, 'Assets'), exist_ok=True)
    dock_im.save(os.path.join(icon, 'Assets', 'record.png'))
    doc = {
        'fill-specializations': [
            {'value': {'solid': 'extended-gray:0.97000,1.00000'}},
            {'appearance': 'dark', 'value': {'solid': 'extended-gray:0.12000,1.00000'}},
        ],
        'groups': [{'layers': [{'image-name': 'record.png', 'name': 'record'}]}],
        'supported-platforms': {'squares': 'shared'},
    }
    with open(os.path.join(icon, 'icon.json'), 'w', encoding='utf-8', newline='\n') as f:
        json.dump(doc, f, indent=2)
        f.write('\n')


if __name__ == '__main__':
    main()
