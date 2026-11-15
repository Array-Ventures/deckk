package browserskill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestProviderCaptureToPDF(t *testing.T) {
	python := os.Getenv("DECKK_TEST_PYTHON")
	if python == "" {
		t.Skip("Set DECKK_TEST_PYTHON to a Python environment with scripts/requirements.txt to run browser-to-PDF integration")
	}
	source, err := os.ReadFile("../../skills/deckk/scripts/viewer_inspect.js")
	if err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	helper, err := filepath.Abs("../../skills/deckk/scripts/pdf_files.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"papermark", "pitch", "canva"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("Unexpected request: %s", r.Method)
				}
				if r.URL.Path == "/slide.svg" {
					w.Header().Set("Content-Type", "image/svg+xml")
					color := "blue"
					if r.URL.Query().Get("page") == "3" {
						color = "green"
					}
					fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360"><rect width="640" height="360" fill="%s"/></svg>`, color)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				root := `<div id="slide" style="position:relative;width:640px;height:360px"`
				switch provider {
				case "pitch":
					root += ` class="slide-wrapper"`
				case "canva":
					root += ` data-page-id="0"`
				}
				root += `><img class="viewer-image-mobile" alt="Page 1" src="/slide.svg?page=1" width="640" height="360"><div style="position:absolute;top:0;left:0;width:100px;height:40px;background:red">WATERMARK</div></div>`
				// Papermark's watermark wrapper is the immediate image parent; all providers preserve it.
				fmt.Fprint(w, `<style>body{margin:0}img{display:block}</style>`+root+`<div data-page-counter>1 / 3</div><button aria-label="Next slide">Next</button><script>
let n=1;document.querySelector('button').onclick=()=>{if(n===3)return;n++;document.querySelector('[data-page-counter]').textContent=n+' / 3';document.querySelector('#slide').dataset.pageId=String(n-1);const img=document.querySelector('img');img.alt='Page '+n;img.src='/slide.svg?page='+n;document.querySelector('button').disabled=n===3;};</script>`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(browser, 30*time.Second)
			defer cancel()
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			pages := []map[string]interface{}{}
			for index := 1; index <= 3; index++ {
				if err := chromedp.Run(ctx, chromedp.Poll(`document.querySelector('img').complete && document.querySelector('img').naturalWidth>0`, nil)); err != nil {
					t.Fatal(err)
				}
				before, err := inspectProvider(ctx, string(source), provider, "")
				if err != nil {
					t.Fatal(err)
				}
				if before["status"] != "ready" || before["index"] != float64(index) || before["total"] != float64(3) {
					t.Fatalf("before=%v", before)
				}
				paths := []string{}
				for capture := 1; capture <= 2; capture++ {
					var png []byte
					if err := chromedp.Run(ctx, chromedp.Screenshot(before["selector"].(string), &png, chromedp.ByQuery)); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(directory, fmt.Sprintf("page-%d-%d.png", index, capture))
					if err := os.WriteFile(path, png, 0600); err != nil {
						t.Fatal(err)
					}
					paths = append(paths, path)
				}
				after, err := inspectProvider(ctx, string(source), provider, "")
				if err != nil {
					t.Fatal(err)
				}
				pages = append(pages, map[string]interface{}{"index": index, "image": paths[1], "firstImage": paths[0], "before": before, "after": after})
				if index < 3 {
					if err := chromedp.Run(ctx, chromedp.Click("[data-deckk-next]", chromedp.ByQuery)); err != nil {
						t.Fatal(err)
					}
				}
			}
			manifest := filepath.Join(directory, "manifest.json")
			data, _ := json.Marshal(map[string]interface{}{"expectedPages": 3, "pages": pages})
			if err := os.WriteFile(manifest, data, 0600); err != nil {
				t.Fatal(err)
			}
			// Assert pixel stability and watermark preservation, then run the actual helper and inspect all PDF image bytes.
			validation := `import importlib.util,json,sys
from pathlib import Path
from pypdf import PdfReader
spec=importlib.util.spec_from_file_location('pdf_files',sys.argv[1]); pdf=importlib.util.module_from_spec(spec);spec.loader.exec_module(pdf)
p=Path(sys.argv[2]);m=json.loads(p.read_text())
for page in m['pages']:
 a=pdf.image_info(page.pop('firstImage'));b=pdf.image_info(page['image']);assert a['pixelHash']==b['pixelHash'];page['stablePixelHash']=b['pixelHash']
 assert pdf.pixels(page['image']).getpixel((10,30)) == (255,0,0), 'watermark layer missing'
p.write_text(json.dumps(m));result=pdf.assemble(p,p.parent/'deck.pdf');assert result['pages']==3
for entry,page in zip(m['pages'],PdfReader(result['path']).pages):
 assert page.images[0].image.convert('RGB').tobytes()==pdf.pixels(entry['image']).tobytes()
print(json.dumps({'pages':result['pages'],'watermark':True,'pixelsVerified':True}))`
			output, err := exec.Command(python, "-c", validation, helper, manifest).CombinedOutput()
			if err != nil {
				t.Fatalf("PDF integration: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}

func TestNotionScrollCaptureToPDF(t *testing.T) {
	python := os.Getenv("DECKK_TEST_PYTHON")
	if python == "" {
		t.Skip("Set DECKK_TEST_PYTHON for browser-to-PDF integration")
	}
	source, err := os.ReadFile("../../skills/deckk/scripts/viewer_inspect.js")
	if err != nil {
		t.Fatal(err)
	}
	scroll, err := os.ReadFile("../../skills/deckk/scripts/notion_scroll.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<style>body{margin:0}</style><div class="notion-scroller vertical" style="width:640px;height:360px;overflow:auto"><div class="notion-page-content" style="height:900px;background:linear-gradient(red,blue)"><div data-block-id="x">Document with a real bottom</div></div></div>`)
	}))
	defer server.Close()
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	ctx, cancel := context.WithTimeout(browser, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	segments := []map[string]interface{}{}
	for index := 0; index < 4; index++ {
		before, err := inspectProvider(ctx, string(source), "notion", "")
		if err != nil {
			t.Fatal(err)
		}
		if before["status"] != "scroll-ready" {
			t.Fatalf("state=%v", before)
		}
		paths := []string{}
		for capture := 1; capture <= 2; capture++ {
			var png []byte
			if err := chromedp.Run(ctx, chromedp.Screenshot(before["selector"].(string), &png, chromedp.ByQuery)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, fmt.Sprintf("strip-%d-%d.png", index, capture))
			if err := os.WriteFile(path, png, 0600); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, path)
		}
		after, err := inspectProvider(ctx, string(source), "notion", "")
		if err != nil {
			t.Fatal(err)
		}
		segments = append(segments, map[string]interface{}{"image": paths[1], "firstImage": paths[0], "before": before, "after": after})
		if after["atEnd"] == true {
			break
		}
		offset := after["offset"].(float64) + after["viewportHeight"].(float64)
		if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf("(%s)(%f)", scroll, offset), nil)); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(directory, "scroll.json")
	data, _ := json.Marshal(map[string]interface{}{"segments": segments})
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	helper, _ := filepath.Abs("../../skills/deckk/scripts/pdf_files.py")
	validation := `import importlib.util,json,sys
from pathlib import Path
from pypdf import PdfReader
spec=importlib.util.spec_from_file_location('pdf_files',sys.argv[1]);pdf=importlib.util.module_from_spec(spec);spec.loader.exec_module(pdf)
p=Path(sys.argv[2]);m=json.loads(p.read_text())
for entry in m['segments']:
 assert pdf.image_info(entry.pop('firstImage'))['pixelHash']==pdf.image_info(entry['image'])['pixelHash'];entry['stablePixelHash']=pdf.image_info(entry['image'])['pixelHash']
p.write_text(json.dumps(m));r=pdf.assemble_scroll(p,p.parent/'notion.pdf');pages=PdfReader(r['path']).pages
assert [float(p.mediabox.height) for p in pages]==[360,360,180]
assert sum(float(p.mediabox.height) for p in pages)==900
print(json.dumps({'pages':r['pages'],'coverage':900,'overlapCropped':True}))`
	output, err := exec.Command(python, "-c", validation, helper, manifest).CombinedOutput()
	if err != nil {
		t.Fatalf("Notion integration: %v\n%s", err, output)
	}
	t.Log(string(output))
}
