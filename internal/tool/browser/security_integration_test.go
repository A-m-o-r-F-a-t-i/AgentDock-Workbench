//go:build browser_integration

package browser

import (
	"context"
	"github.com/chromedp/chromedp"
	"html"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityDOMParametersRemainLiteral(t *testing.T) {
	value := "\";globalThis.injected=true;//'\\`你好\u2028"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<input id="field"><select id="choice"><option value="other">other</option><option value="` + html.EscapeString(value) + `">selected</option></select><script>globalThis.injected=false</script>`))
	}))
	defer server.Close()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(integrationExecutable(t)))
	allocator, cancel := chromedp.NewExecAllocator(context.Background(), options...)
	defer cancel()
	page, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, cancel := context.WithTimeout(page, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
		t.Fatal(err)
	}
	if err := fillValue(ctx, page, FillAction{Selector: "#field", Value: value}); err != nil {
		t.Fatal(err)
	}
	if err := selectValue(ctx, page, SelectAction{Selector: "#choice", Value: value}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Field    string `json:"field"`
		Choice   string `json:"choice"`
		Injected bool   `json:"injected"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`({field:document.querySelector('#field').value,choice:document.querySelector('#choice').value,injected:globalThis.injected})`, &got)); err != nil {
		t.Fatal(err)
	}
	if got.Field != value || got.Choice != value || got.Injected {
		t.Fatalf("DOM data boundary changed: %+v", got)
	}
}
