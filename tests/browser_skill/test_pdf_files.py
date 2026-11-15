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


if __name__ == '__main__':
    unittest.main()
