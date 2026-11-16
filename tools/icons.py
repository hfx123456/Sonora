"""Draws Sonora's icons. Run on Windows, with Pillow: python tools/icons.py

Every icon is rendered from clean vector geometry:

  resources/icon.svg, icon.png   the app's icon, 1024 px (Linux, macOS's .icns)
  resources/icon-small.svg       bold simplified variant for 16-24 px
  resources/icon.ico             Windows: icon-small for 16-24 px, icon above
  internal/app/tray.png          Windows notification area full-color badge, 64 px
  internal/app/tray-template.png macOS menu bar monochrome template, 32 px
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


def geometric_note(small=False, mono=False):
    """Flat Bauhaus/Swiss geometric note + acoustic bars."""
    stem = '#000000' if mono else '#111827'
    mint = '#000000' if mono else '#18C964'
    sky = '#000000' if mono else '#0EA5E9'
    if small:
        return (
            f'<rect x="466" y="236" width="108" height="432" rx="54" fill="{stem}"/>'
            f'<path d="M518 236 L752 312 C780 321 791 349 782 377 C773 403 747 416 721 407 L518 342 Z" fill="{stem}"/>'
            f'<circle cx="416" cy="656" r="158" fill="{mint}"/>'
            f'<rect x="182" y="330" width="94" height="196" rx="47" fill="{mint}"/>'
            f'<rect x="708" y="460" width="94" height="258" rx="47" fill="{sky}"/>'
        )
    return (
        f'<rect x="474" y="256" width="96" height="404" rx="48" fill="{stem}"/>'
        f'<path d="M522 256 L732 324 C758 332 768 358 760 384 C752 408 728 420 704 412 L522 354 Z" fill="{stem}"/>'
        f'<circle cx="426" cy="648" r="144" fill="{mint}"/>'
        f'<rect x="216" y="356" width="80" height="176" rx="40" fill="{mint}"/>'
        f'<rect x="696" y="472" width="80" height="236" rx="40" fill="{sky}"/>'
    )


def tray_template_note():
    """Clean, well-separated monochrome note silhouette for macOS menu bar."""
    return (
        '<rect x="468" y="196" width="112" height="492" rx="56" fill="#000000"/>'
        '<path d="M524 196 L780 278 C812 288 824 322 814 352 C804 380 774 394 744 384 L524 314 Z" fill="#000000"/>'
        '<circle cx="404" cy="680" r="176" fill="#000000"/>'
    )


def app_icon(small=False):
    if small:
        body = (
            '<rect x="44" y="44" width="936" height="936" rx="236" fill="#F4F6F4" stroke="#D7E0DC" stroke-width="18"/>'
            + geometric_note(small=True)
        )
    else:
        body = (
            '<rect x="64" y="64" width="896" height="896" rx="224" fill="#F4F6F4" stroke="#DFE6E2" stroke-width="8"/>'
            + geometric_note(small=False)
        )
    return svg(body)


def svg(body, defs=''):
    if defs:
        return (f'<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">'
                f'<defs>{defs}</defs>{body}</svg>\n')
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">'
            f'{body}</svg>\n')


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
    # Monochrome template for macOS menu bar: clean single note without overlapping bars.
    tray = svg(tray_template_note())
    dock = svg(
        f'<g transform="translate({C} {C}) scale(1.06) translate(-{C} -{C})">'
        f'{geometric_note(small=False)}</g>'
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

    # Windows tray uses the full-color 64 px badge matching the app icon; macOS uses the monochrome template.
    sized(small_im, 64).save(os.path.join(ROOT, 'internal/app/tray.png'))
    t = sized(tray_im, 32)
    black = Image.new('L', t.size, 0)
    Image.merge('RGBA', (black, black, black, t.getchannel('A'))).save(os.path.join(ROOT, 'internal/app/tray-template.png'))

    icon = os.path.join(ROOT, 'AppIcon.icon')
    os.makedirs(os.path.join(icon, 'Assets'), exist_ok=True)
    dock_im.save(os.path.join(icon, 'Assets', 'record.png'))
    doc = {
        'fill-specializations': [
            {'value': {'solid': 'extended-gray:0.96000,1.00000'}},
        ],
        'groups': [{'layers': [{'image-name': 'record.png', 'name': 'record'}]}],
        'supported-platforms': {'squares': 'shared'},
    }
    with open(os.path.join(icon, 'icon.json'), 'w', encoding='utf-8', newline='\n') as f:
        json.dump(doc, f, indent=2)
        f.write('\n')


if __name__ == '__main__':
    main()
