#!/usr/bin/env python3
"""Validate downloaded PDFs and assemble verified browser slide captures locally."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import tempfile

from PIL import Image, ImageOps
from pypdf import PdfReader
from reportlab.pdfgen import canvas
from reportlab.lib.utils import ImageReader


def pixels(path):
    with Image.open(path) as source:
        source.load()
        image = ImageOps.exif_transpose(source).convert('RGBA')
        background = Image.new('RGBA', image.size, 'white')
        background.alpha_composite(image)
        return background.convert('RGB')


def image_info(path):
    image = pixels(path)
    digest = hashlib.sha256()
    digest.update(f'{image.width}x{image.height}:RGB:'.encode())
    digest.update(image.tobytes())
    return {'path': str(Path(path).resolve()), 'width': image.width,
            'height': image.height, 'pixelHash': digest.hexdigest()}


def inspect_pdf(path, expected=None):
    path = Path(path)
    with path.open('rb') as stream:
        if stream.read(5) != b'%PDF-':
            raise ValueError('Downloaded file is not a PDF')
        stream.seek(max(0, path.stat().st_size - 1024))
        if b'%%EOF' not in stream.read():
            raise ValueError('PDF is missing its end marker')
    reader = PdfReader(path, strict=True)
    if reader.is_encrypted:
        raise ValueError('Encrypted PDF requires manual handling')
    count = len(reader.pages)
    if count < 1 or (expected is not None and count != expected):
        raise ValueError(f'PDF has {count} pages; expected {expected or "at least one"}')
    # Resolve every page, including its content stream, before declaring success.
    for page in reader.pages:
        content = page.get_contents()
        if content is not None:
            content.get_data()
    with path.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    return {'path': str(path.resolve()), 'pages': count,
            'bytes': path.stat().st_size, 'sha256': digest}


def assemble(manifest_path, output):
    manifest_path = Path(manifest_path).resolve()
    manifest = json.loads(manifest_path.read_text())
    count = manifest.get('expectedPages')
    if type(count) is not int or not 1 <= count <= 300:
        raise ValueError('expectedPages must be an integer from 1 to 300')
    pages = manifest.get('pages')
    if not isinstance(pages, list) or len(pages) != count:
        raise ValueError('Manifest must contain every expected page exactly once')
    identity = None
    images = []
    for index, page in enumerate(pages, 1):
        if page.get('index') != index:
            raise ValueError('Manifest pages must be ordered consecutively from 1')
        before, after = page['before'], page['after']
        for state in (before, after):
            if state.get('status') != 'ready' or state.get('index') != index or state.get('total') != count:
                raise ValueError(f'Page {index} was not ready at the expected index and total')
            current = (state.get('origin'), state.get('documentPath'))
            if not all(isinstance(value, str) and value for value in current):
                raise ValueError('Missing document identity')
            if identity is not None and current != identity:
                raise ValueError('Document changed during capture')
            identity = current
        fingerprint = before.get('fingerprint')
        if not fingerprint or fingerprint != after.get('fingerprint'):
            raise ValueError(f'Page {index} changed during capture')
        image_path = manifest_path.parent / page['image']
        if image_info(image_path)['pixelHash'] != page.get('stablePixelHash'):
            raise ValueError(f'Page {index} pixels do not match the stable capture')
        images.append(image_path)
    return publish([(path, None) for path in images], output)


def assemble_scroll(manifest_path, output):
    """Validate ordered overlapping viewport captures, cropping overlap and footer padding."""
    manifest_path = Path(manifest_path).resolve()
    segments = json.loads(manifest_path.read_text()).get('segments')
    if not isinstance(segments, list) or not 1 <= len(segments) <= 300:
        raise ValueError('Scroll manifest needs 1 to 300 segments')
    identity = None
    height = None
    covered = 0
    dimensions = None
    images = []
    for segment in segments:
        before, after = segment['before'], segment['after']
        for key in ('origin', 'documentPath', 'contentHeight', 'viewportHeight', 'offset', 'fingerprint'):
            if before.get(key) != after.get(key):
                raise ValueError('Document moved or changed during a scroll capture')
        if before.get('status') != 'scroll-ready' or after.get('status') != 'scroll-ready' or before.get('collapsed') or after.get('collapsed'):
            raise ValueError('Scroll capture is not ready or has collapsed content')
        current = (before.get('origin'), before.get('documentPath'))
        if not all(isinstance(value, str) and value for value in current) or not before.get('fingerprint'):
            raise ValueError('Missing scroll document identity or fingerprint')
        if identity is not None and current != identity:
            raise ValueError('Scroll document changed')
        identity = current
        offset, viewport, current_height = (before.get(key) for key in ('offset', 'viewportHeight', 'contentHeight'))
        if not all(type(value) in (int, float) and math.isfinite(value) for value in (offset, viewport, current_height)):
            raise ValueError('Invalid scroll dimensions')
        if not 0 <= offset < current_height <= 40000 or viewport < 100:
            raise ValueError('Scroll dimensions are outside the supported range')
        if height is not None and abs(height - current_height) > 0.5:
            raise ValueError('Scroll document height changed; restart the capture')
        height = current_height
        if offset > covered + 1e-6 or offset + viewport <= covered:
            raise ValueError('Scroll captures have a gap or make no progress')
        if covered == 0 and offset != 0:
            raise ValueError('Scroll capture must start at the document top')
        image_path = manifest_path.parent / segment['image']
        if image_info(image_path)['pixelHash'] != segment.get('stablePixelHash'):
            raise ValueError('Scroll capture pixels changed')
        image = pixels(image_path)
        scale = image.height / viewport
        if dimensions is not None and (image.width != dimensions[0] or abs(scale - dimensions[1]) > 0.001):
            raise ValueError('Scroll capture width or pixel scale changed')
        dimensions = (image.width, scale)
        top = round((covered - offset) * scale)
        bottom = round((min(height, offset + viewport) - offset) * scale)
        if bottom <= top:
            raise ValueError('Scroll capture has no new pixels')
        images.append((image_path, (0, top, image.width, bottom)))
        covered = min(height, offset + viewport)
    if height is None or covered < height - 0.5 or not segments[-1]['after'].get('atEnd'):
        raise ValueError('Scroll capture did not reach the document end')
    return publish(images, output)


def publish(images, output):
    output = Path(output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        raise FileExistsError(f'Output already exists: {output}')
    descriptor, temporary = tempfile.mkstemp(prefix='.deckk-', suffix='.pdf', dir=output.parent)
    os.close(descriptor)
    try:
        pdf = canvas.Canvas(temporary, pageCompression=1)
        for path, crop in images:
            image = pixels(path)
            if crop is not None:
                image = image.crop(crop)
            pdf.setPageSize(image.size)
            pdf.drawImage(ImageReader(image), 0, 0, width=image.width, height=image.height)
            pdf.showPage()
        pdf.save()
        inspect_pdf(temporary, len(images))
        # Exclusive publication prevents replacing an existing user file, including races.
        os.link(temporary, output)
        return inspect_pdf(output, len(images))
    finally:
        Path(temporary).unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    info = commands.add_parser('image-info')
    info.add_argument('images', nargs='+')
    inspect = commands.add_parser('inspect')
    inspect.add_argument('pdf')
    inspect.add_argument('--expected-pages', type=int)
    build = commands.add_parser('assemble')
    build.add_argument('manifest')
    build.add_argument('output')
    scroll = commands.add_parser('assemble-scroll')
    scroll.add_argument('manifest')
    scroll.add_argument('output')
    args = parser.parse_args()
    try:
        if args.command == 'image-info':
            result = [image_info(path) for path in args.images]
        elif args.command == 'inspect':
            result = inspect_pdf(args.pdf, args.expected_pages)
        elif args.command == 'assemble-scroll':
            result = assemble_scroll(args.manifest, args.output)
        else:
            result = assemble(args.manifest, args.output)
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(1, f'deckk: {error}\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
