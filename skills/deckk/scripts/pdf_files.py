#!/usr/bin/env python3
"""Validate downloaded PDFs and assemble verified browser slide captures locally."""
import argparse
import hashlib
import json
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
    output = Path(output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        raise FileExistsError(f'Output already exists: {output}')
    descriptor, temporary = tempfile.mkstemp(prefix='.deckk-', suffix='.pdf', dir=output.parent)
    os.close(descriptor)
    try:
        pdf = canvas.Canvas(temporary, pageCompression=1)
        for path in images:
            image = pixels(path)
            pdf.setPageSize(image.size)
            pdf.drawImage(ImageReader(image), 0, 0, width=image.width, height=image.height)
            pdf.showPage()
        pdf.save()
        inspect_pdf(temporary, count)
        # Exclusive publication prevents replacing an existing user file, including races.
        os.link(temporary, output)
        return inspect_pdf(output, count)
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
    args = parser.parse_args()
    try:
        if args.command == 'image-info':
            result = [image_info(path) for path in args.images]
        elif args.command == 'inspect':
            result = inspect_pdf(args.pdf, args.expected_pages)
        else:
            result = assemble(args.manifest, args.output)
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(1, f'deckk: {error}\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
