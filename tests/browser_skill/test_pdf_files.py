import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

from PIL import Image
from pypdf import PdfReader, PdfWriter

spec = importlib.util.spec_from_file_location('pdf_files', Path(__file__).resolve().parents[2] / 'skills/deckk/scripts/pdf_files.py')
pdf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pdf)


class CapturePDFTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.manifest = {'expectedPages': 3, 'pages': []}
        for index, color in enumerate(('red', 'green', 'blue'), 1):
            path = self.root / f'{index}.png'
            Image.new('RGB', (400 + index, 250), color).save(path)
            state = {'status': 'ready', 'index': index, 'total': 3,
                     'origin': 'http://localhost', 'documentPath': '/view/test', 'fingerprint': f'source-{index}'}
            self.manifest['pages'].append({'index': index, 'image': path.name,
                'before': dict(state), 'after': dict(state), 'stablePixelHash': pdf.image_info(path)['pixelHash']})

    def build(self, manifest=None):
        path = self.root / 'manifest.json'
        path.write_text(json.dumps(manifest or self.manifest))
        return pdf.assemble(path, self.root / 'deck.pdf')

    def test_page_order_dimensions_and_embedded_pixels(self):
        result = self.build()
        self.assertEqual(result['pages'], 3)
        reader = PdfReader(result['path'])
        for index, page in enumerate(reader.pages, 1):
            self.assertEqual(float(page.mediabox.width), 400 + index)
            image = page.images[0].image.convert('RGB')
            self.assertEqual(image.tobytes(), pdf.pixels(self.root / f'{index}.png').tobytes())

    def test_incomplete_out_of_order_changed_and_wrong_document_rejected(self):
        cases = []
        missing = copy.deepcopy(self.manifest)
        missing['pages'].pop()
        cases.append(missing)
        order = copy.deepcopy(self.manifest)
        order['pages'].reverse()
        cases.append(order)
        changed = copy.deepcopy(self.manifest)
        changed['pages'][0]['after']['fingerprint'] = 'different'
        cases.append(changed)
        identity = copy.deepcopy(self.manifest)
        identity['pages'][1]['before']['documentPath'] = '/view/other'
        cases.append(identity)
        for manifest in cases:
            with self.subTest(manifest=manifest), self.assertRaises(ValueError):
                self.build(manifest)
            self.assertFalse((self.root / 'deck.pdf').exists())

    def test_modified_image_rejected(self):
        Image.new('RGB', (401, 250), 'black').save(self.root / '1.png')
        with self.assertRaisesRegex(ValueError, 'pixels'):
            self.build()

    def test_existing_output_preserved(self):
        target = self.root / 'deck.pdf'
        target.write_bytes(b'keep me')
        with self.assertRaises(FileExistsError):
            self.build()
        self.assertEqual(target.read_bytes(), b'keep me')

    def test_native_html_truncation_count_and_encryption(self):
        path = self.root / 'native.pdf'
        path.write_bytes(b'<html>sign in</html>')
        with self.assertRaises(ValueError):
            pdf.inspect_pdf(path)
        result = self.build()
        with self.assertRaises(ValueError):
            pdf.inspect_pdf(result['path'], 4)
        path.write_bytes(Path(result['path']).read_bytes()[:-40])
        with self.assertRaises(ValueError):
            pdf.inspect_pdf(path)
        writer = PdfWriter()
        writer.add_blank_page(200, 100)
        writer.encrypt('dummy-test-only')
        with path.open('wb') as stream:
            writer.write(stream)
        with self.assertRaisesRegex(ValueError, 'Encrypted'):
            pdf.inspect_pdf(path)

    def test_identical_verified_slides_are_valid(self):
        for index in (2, 3):
            (self.root / f'{index}.png').write_bytes((self.root / '1.png').read_bytes())
            self.manifest['pages'][index-1]['stablePixelHash'] = pdf.image_info(self.root / f'{index}.png')['pixelHash']
        self.assertEqual(self.build()['pages'], 3)


class ScrollPDFTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.document = Image.new('RGB', (400, 900))
        for y in range(900):
            for x in range(400):
                self.document.putpixel((x, y), (y % 256, (y // 3) % 256, x % 256))
        self.manifest = {'segments': []}
        for index, offset in enumerate((0, 300)):
            image = self.document.crop((0, offset, 400, offset + 600))
            path = self.root / f'{index}.png'
            image.save(path)
            state = {'status': 'scroll-ready', 'origin': 'https://example.test',
                     'documentPath': '/notion', 'offset': offset, 'viewportHeight': 600,
                     'contentHeight': 900, 'fingerprint': f'strip-{index}',
                     'collapsed': 0, 'atEnd': index == 1}
            self.manifest['segments'].append({'image': path.name, 'before': dict(state),
                'after': dict(state), 'stablePixelHash': pdf.image_info(path)['pixelHash']})

    def build(self, manifest=None):
        path = self.root / 'scroll.json'
        path.write_text(json.dumps(manifest or self.manifest))
        return pdf.assemble_scroll(path, self.root / 'scroll.pdf')

    def test_overlap_is_cropped_and_embedded_pixels_cover_document_once(self):
        result = self.build()
        reader = PdfReader(result['path'])
        self.assertEqual(result['pages'], 2)
        self.assertEqual([float(page.mediabox.height) for page in reader.pages], [600, 300])
        images = [page.images[0].image.convert('RGB') for page in reader.pages]
        joined = Image.new('RGB', (400, 900))
        joined.paste(images[0], (0, 0))
        joined.paste(images[1], (0, 600))
        self.assertEqual(joined.tobytes(), self.document.tobytes())

    def test_incomplete_gap_height_change_movement_and_collapsed_rejected(self):
        cases = []
        incomplete = copy.deepcopy(self.manifest)
        incomplete['segments'].pop()
        cases.append(incomplete)
        for key, value in [('offset', 650), ('contentHeight', 950), ('collapsed', 1)]:
            changed = copy.deepcopy(self.manifest)
            changed['segments'][1]['before'][key] = value
            changed['segments'][1]['after'][key] = value
            cases.append(changed)
        moved = copy.deepcopy(self.manifest)
        moved['segments'][0]['after']['offset'] = 1
        cases.append(moved)
        for manifest in cases:
            with self.subTest(manifest=manifest), self.assertRaises(ValueError):
                self.build(manifest)
            self.assertFalse((self.root / 'scroll.pdf').exists())

    def test_nonfinite_dimensions_and_changed_pixels_rejected(self):
        invalid = copy.deepcopy(self.manifest)
        invalid['segments'][0]['before']['contentHeight'] = float('inf')
        invalid['segments'][0]['after']['contentHeight'] = float('inf')
        with self.assertRaises(ValueError):
            self.build(invalid)
        Image.new('RGB', (400, 600), 'black').save(self.root / '0.png')
        with self.assertRaisesRegex(ValueError, 'pixels'):
            self.build()


if __name__ == '__main__':
    unittest.main()
