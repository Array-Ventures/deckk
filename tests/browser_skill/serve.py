"""Local DocSend-shaped fixture. Run from a directory containing 1.png..3.png and native.pdf."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        values = parse_qs(self.rfile.read(int(self.headers.get('Content-Length', '0'))).decode())
        if values.get('email') == ['viewer@example.test'] and values.get('passcode') == ['fixture-only']:
            self.send_response(303)
            self.send_header('Set-Cookie', 'fixture=authorized; Path=/; SameSite=Lax')
            self.send_header('Location', self.path)
            self.end_headers()
        else:
            self.send_response(403)
            self.end_headers()
            self.wfile.write(b'Incorrect fixture credentials')

    def do_GET(self):
        url = urlparse(self.path)
        path = url.path
        if path in ('/1.png', '/2.png', '/3.png', '/native.pdf'):
            data = Path(path.lstrip('/')).read_bytes()
            self.send_response(200)
            self.send_header('Content-Type', 'application/pdf' if path.endswith('.pdf') else 'image/png')
            self.send_header('Content-Length', str(len(data)))
            if path.endswith('.pdf'):
                self.send_header('Content-Disposition', 'attachment; filename="native.pdf"')
            self.end_headers()
            self.wfile.write(data)
            return
        if 'fixture=authorized' not in self.headers.get('Cookie', ''):
            html = '<form method="post"><label>Email<input type="email" name="email"></label><label>Passcode<input type="password" name="passcode"></label><button>Continue</button></form>'
        elif parse_qs(url.query).get('gate') == ['nda']:
            html = '<div class="nda-agreement">Dummy NDA<button>I agree</button></div>'
        else:
            download = '<a href="/native.pdf" download>Download PDF</a>' if parse_qs(url.query).get('native') == ['1'] else ''
            html = '''<style>body{margin:0;background:#ddd}img{display:block;width:640px;height:360px}button,a{margin:8px}</style>
<div data-page-num="1"><img class="page-view" src="/1.png"></div>
<div data-page-counter>1 / 3</div><button aria-label="Previous slide" disabled>Previous</button><button aria-label="Next slide">Next</button>''' + download + '''
<script>
let index=1;
function show(next){if(next<1||next>3)return;index=next;const img=document.querySelector('img');img.src='/'+index+'.png';img.parentElement.dataset.pageNum=String(index);document.querySelector('[data-page-counter]').textContent=index+' / 3';document.querySelector('[aria-label="Previous slide"]').disabled=index===1;document.querySelector('[aria-label="Next slide"]').disabled=index===3;}
document.querySelector('[aria-label="Previous slide"]').onclick=()=>show(index-1);
document.querySelector('[aria-label="Next slide"]').onclick=()=>show(index+1);
document.onkeydown=e=>{if(e.key==='ArrowRight')show(index+1);if(e.key==='ArrowLeft')show(index-1)};
</script>'''
        self.send_response(200)
        self.send_header('Content-Type', 'text/html')
        self.end_headers()
        self.wfile.write(html.encode())


if __name__ == '__main__':
    ThreadingHTTPServer(('127.0.0.1', 18228), Handler).serve_forever()
