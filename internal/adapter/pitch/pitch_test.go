package pitch

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/holman/deckk/internal/adapter"
)

func TestCompleteCapture(t *testing.T) {
	for _, mode := range []string{"duplicate", "vanished", "stuck", "start-late"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintf(w, `<!doctype html><style>.slide-wrapper{width:640px;height:360px;background:blue}</style>
<div class="slide-wrapper"></div><div class="player-v2-chrome"><span class="player-v2-chrome-controls-slide-count">1 / 2</span></div>
<script>const mode=%q;const count=document.querySelector('span');if(mode==='start-late')count.textContent='2 / 2';
document.onkeydown=e=>{if(e.key==='ArrowRight'){
 if(mode==='vanished')document.querySelector('.slide-wrapper').remove();
 else if(mode!=='stuck')count.textContent='2 / 2';
}}</script>`, mode)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			slides, err := (&Adapter{}).Fetch(ctx, server.URL, adapter.Options{})
			if mode != "duplicate" {
				if err == nil || len(slides) != 0 {
					t.Fatalf("partial or incorrect deck returned: slides=%d err=%v", len(slides), err)
				}
				return
			}
			if err != nil || len(slides) != 2 || !bytes.Equal(slides[0].Data, slides[1].Data) {
				t.Fatalf("intentional duplicate page dropped: slides=%d err=%v", len(slides), err)
			}
		})
	}
}
