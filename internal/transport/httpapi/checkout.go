package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
)

type checkoutPage struct {
	TransactionID string
	StatusURL     string
	Status        string
	Amount        string
	Currency      string
	ExpiresAt     string
	QRImage       template.URL
	RedirectURL   string
	BankTransfer  *checkoutBankTransfer
	SuccessURL    string
	CancelURL     string
	Nonce         string
}

type checkoutBankTransfer struct {
	Rail         string
	AccountLabel string
	AccountValue string
	SingleUse    bool
}

func (s *Server) checkoutPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("transactionId")
	if !validUUID(id) || s.payments == nil {
		http.NotFound(w, r)
		return
	}
	payment, err := s.payments.Checkout(r.Context(), id)
	if err != nil {
		if !errors.Is(err, app.ErrNotFound) && !errors.Is(err, core.ErrNotFound) {
			writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "The requested service is temporarily unavailable.")
			return
		}
		http.NotFound(w, r)
		return
	}
	nonce := checkoutNonce()
	page := checkoutPage{
		TransactionID: payment.TransactionID,
		StatusURL:     strings.TrimSuffix(r.URL.Path, "/pay/"+payment.TransactionID) + "/public/v1/checkout/payments/" + payment.TransactionID + "/status",
		Status:        payment.Status,
		Amount:        payment.Amount,
		Currency:      payment.Currency,
		ExpiresAt:     payment.ExpirationDate.UTC().Format(time.RFC3339),
		QRImage:       checkoutQRImage(payment.PaymentData),
		RedirectURL:   checkoutRedirect(payment.PaymentData),
		BankTransfer:  checkoutBankDetails(payment.PaymentData),
		SuccessURL:    checkoutReturnURL(payment.SuccessURL),
		CancelURL:     checkoutReturnURL(payment.CancelURL),
		Nonce:         nonce,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; connect-src 'self'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	if err = checkoutTemplate.Execute(w, page); err != nil {
		return
	}
}

func checkoutBankDetails(paymentData map[string]any) *checkoutBankTransfer {
	if typeName, _ := paymentData["type"].(string); typeName != "bank_transfer" {
		return nil
	}
	bank, _ := paymentData["bankTransfer"].(map[string]any)
	identifier, _ := bank["accountIdentifier"].(map[string]any)
	identifierType, _ := identifier["type"].(string)
	value, _ := identifier["value"].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	label := strings.ToUpper(strings.TrimSpace(identifierType))
	if label == "" {
		label = "Cuenta bancaria"
	}
	rail, _ := bank["rail"].(string)
	destinationMode, _ := bank["destinationMode"].(string)
	return &checkoutBankTransfer{
		Rail:         strings.ToUpper(strings.TrimSpace(rail)),
		AccountLabel: label,
		AccountValue: value,
		SingleUse:    destinationMode == "single_use",
	}
}

func (s *Server) checkoutStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("transactionId")
	if !validUUID(id) || s.payments == nil {
		http.NotFound(w, r)
		return
	}
	payment, err := s.payments.Checkout(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := fmt.Sprintf(`"%d-%s"`, payment.Version, payment.Status)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"transactionId":   payment.TransactionID,
		"status":          payment.Status,
		"expirationDate":  payment.ExpirationDate,
		"resourceVersion": payment.Version,
	})
}

func checkoutNonce() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "dinaria-checkout"
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func checkoutQRImage(paymentData map[string]any) template.URL {
	qr, _ := paymentData["qr"].(map[string]any)
	encoded, _ := qr["imageBase64"].(string)
	encoded = strings.TrimSpace(encoded)
	if comma := strings.IndexByte(encoded, ','); strings.HasPrefix(encoded, "data:") && comma >= 0 {
		encoded = encoded[comma+1:]
	}
	if encoded == "" {
		return ""
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		return ""
	}
	return template.URL("data:image/png;base64," + encoded) // #nosec G203 -- decoded base64 is constrained to a PNG data URL.
}

func checkoutRedirect(paymentData map[string]any) string {
	redirect, _ := paymentData["redirect"].(map[string]any)
	links, _ := redirect["links"].(map[string]any)
	preferred, _ := redirect["recommendedAlternative"].(string)
	order := []string{preferred, "universal", "web", "app"}
	for _, key := range order {
		value, _ := links[key].(string)
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
			return value
		}
	}
	return ""
}

func checkoutReturnURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return value
}

var checkoutTemplate = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>Pagá con Dinaria</title><style nonce="{{.Nonce}}">
:root{color-scheme:light;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f4f7f6;color:#10231c}*{box-sizing:border-box}body{margin:0;min-height:100svh;display:grid;place-items:center;padding:20px}.card{width:min(100%,440px);background:#fff;border:1px solid #dfe8e4;border-radius:24px;padding:28px;box-shadow:0 18px 60px rgba(20,55,43,.10)}.brand{font-weight:800;letter-spacing:.08em;color:#087a55;text-align:center}.eyebrow{text-align:center;color:#5d6f68;font-size:14px;margin:22px 0 4px}h1{text-align:center;font-size:30px;margin:0}.amount{text-align:center;font-size:26px;font-weight:750;margin:10px 0 8px}.amount-copy{display:block;margin:0 auto 20px}.qr{display:block;width:min(100%,280px);aspect-ratio:1;margin:auto;border:12px solid #fff;border-radius:16px;box-shadow:0 0 0 1px #dfe8e4}.steps{margin:22px 0 16px;padding-left:22px;color:#334b42;line-height:1.55}.bank{margin:20px 0}.badge{width:max-content;margin:0 auto 14px;padding:6px 10px;border-radius:999px;background:#edf8f3;color:#08653f;font-size:13px;font-weight:750}.instruction{text-align:center;color:#334b42;line-height:1.5}.field{margin-top:14px;padding:14px;border:1px solid #dfe8e4;border-radius:14px;background:#f8fbfa}.field-label{display:block;color:#687972;font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.04em}.field-row{display:flex;align-items:center;gap:10px;margin-top:6px}.field-value{min-width:0;flex:1;font:700 16px ui-monospace,SFMono-Regular,Menlo,monospace;overflow-wrap:anywhere}.copy{border:0;border-radius:9px;padding:9px 11px;background:#e4f3ed;color:#08653f;font-weight:750;cursor:pointer}.notice{margin:14px 0 0;padding:12px;border-radius:12px;background:#fff8e7;color:#684f12;font-size:13px;line-height:1.45}.timer,.status{text-align:center}.timer{color:#687972;font-size:14px}.status{margin-top:14px;padding:12px;border-radius:12px;background:#edf8f3;font-weight:700}.status[data-state="confirmed"]{background:#e0f7eb;color:#08653f}.status[data-state="expired"],.status[data-state="cancelled"],.status[data-state="failed"]{background:#fff0ef;color:#9a2920}.action{display:block;margin-top:18px;padding:14px;border-radius:12px;text-align:center;text-decoration:none;background:#087a55;color:white;font-weight:750}.hidden{display:none}.ref{text-align:center;color:#819089;font-size:11px;margin-top:18px;overflow-wrap:anywhere}@media(max-width:380px){.card{padding:20px;border-radius:18px}h1{font-size:26px}.qr{width:240px}.field-row{align-items:flex-start;flex-direction:column}.field-row .copy{width:100%}}
</style></head><body><main class="card"><div class="brand">DINARIA</div><p class="eyebrow">Pago seguro</p><h1 id="title">{{if .BankTransfer}}Transferí para pagar{{else}}Pagá con QR{{end}}</h1><div class="amount" id="amount" data-amount="{{.Amount}}" data-currency="{{.Currency}}">{{.Currency}} {{.Amount}}</div>{{if .BankTransfer}}<button class="copy amount-copy" type="button" data-copy-value="{{.Amount}}">Copiar monto</button>{{end}}
{{if .QRImage}}<img class="qr" src="{{.QRImage}}" width="280" height="280" alt="Código QR para realizar el pago"><ol class="steps"><li>Abrí la aplicación de tu banco o billetera.</li><li>Elegí <strong>Pagar con QR</strong>.</li><li>Escaneá el código y confirmá el importe.</li></ol>{{end}}
{{if .RedirectURL}}<a class="action" id="redirect" href="{{.RedirectURL}}" rel="noopener">Continuar al pago</a>{{end}}
{{with .BankTransfer}}<section class="bank" id="bank-transfer" data-single-use="{{.SingleUse}}"><div class="badge">{{if .SingleUse}}{{.AccountLabel}} de un solo uso{{else}}Transferencia bancaria{{end}}</div><p class="instruction">Realizá una transferencia {{if .Rail}}por {{.Rail}} {{end}}a la siguiente {{.AccountLabel}}.</p><div class="field"><span class="field-label">{{.AccountLabel}}</span><div class="field-row"><span class="field-value" id="bank-account">{{.AccountValue}}</span><button class="copy" type="button" data-copy="bank-account">Copiar</button></div></div>{{if .SingleUse}}<p class="notice">Esta {{.AccountLabel}} corresponde únicamente a este pago. Transferí el importe exacto antes del vencimiento y no la reutilices.</p>{{end}}</section>{{end}}
{{if .SuccessURL}}<a class="action hidden" id="success-return" href="{{.SuccessURL}}" rel="noopener">Volver al comercio</a>{{end}}
{{if .CancelURL}}<a class="action hidden" id="cancel-return" href="{{.CancelURL}}" rel="noopener">Volver al comercio</a>{{end}}
<p class="timer" id="timer" data-expires="{{.ExpiresAt}}"></p><div class="status" id="status" data-state="{{.Status}}">Esperando el pago</div><div class="ref">Operación {{.TransactionID}}</div></main>
<script nonce="{{.Nonce}}">(()=>{const statusEl=document.getElementById('status'),timer=document.getElementById('timer'),title=document.getElementById('title'),qr=document.querySelector('.qr'),steps=document.querySelector('.steps'),bank=document.getElementById('bank-transfer'),redirect=document.getElementById('redirect'),successReturn=document.getElementById('success-return'),cancelReturn=document.getElementById('cancel-return'),expires=new Date(timer.dataset.expires),terminal=new Set(['confirmed','expired','cancelled','failed']),isBank=!!bank;let etag='',started=Date.now(),stopped=false,redirectScheduled=false,timerId,pollId;document.querySelectorAll('[data-copy],[data-copy-value]').forEach(button=>button.addEventListener('click',async()=>{const target=button.dataset.copy?document.getElementById(button.dataset.copy):null,value=button.dataset.copyValue||(target?target.textContent.trim():'');if(!value)return;try{await navigator.clipboard.writeText(value);const original=button.textContent;button.textContent='Copiado';setTimeout(()=>button.textContent=original,1600)}catch{}}));const text={started:'Esperando el pago',pending:'Esperando el pago',confirmed:'Pago confirmado',expired:'El pago venció',cancelled:'Pago cancelado',failed:'No se pudo completar el pago'};function render(state){statusEl.dataset.state=state;statusEl.textContent=text[state]||'Procesando el pago';if(terminal.has(state)){stopped=true;clearTimeout(pollId);clearInterval(timerId);timer.textContent='';if(state==='confirmed'){title.textContent='¡Pago confirmado!';if(successReturn){successReturn.classList.remove('hidden');if(!redirectScheduled){redirectScheduled=true;statusEl.textContent='Pago confirmado. Volviendo al comercio…';setTimeout(()=>location.assign(successReturn.href),3000)}}}else if(cancelReturn)cancelReturn.classList.remove('hidden');if(qr)qr.classList.add('hidden');if(steps)steps.classList.add('hidden');if(bank)bank.classList.add('hidden');if(redirect)redirect.classList.add('hidden')}}function tick(){const left=Math.max(0,expires-Date.now());if(!Number.isFinite(left)){timer.textContent='';return}const m=Math.floor(left/60000),s=Math.floor(left%60000/1000);timer.textContent=left?(isBank?'El pago vence en ':'El QR vence en ')+String(m).padStart(2,'0')+':'+String(s).padStart(2,'0'):(isBank?'El pago ha vencido':'El QR ha vencido');if(!left&&!stopped)render('expired')}async function poll(){if(stopped||document.hidden)return;try{const headers=etag?{'If-None-Match':etag}:{};const response=await fetch('{{.StatusURL}}',{headers,cache:'no-store'});if(response.status===304)return;if(response.ok){etag=response.headers.get('ETag')||etag;const data=await response.json();render(data.status)}}catch{}finally{if(!stopped){const elapsed=Date.now()-started,delay=elapsed<60000?4000:elapsed<300000?8000:15000;pollId=setTimeout(poll,delay+Math.random()*750)}}}document.addEventListener('visibilitychange',()=>{if(!document.hidden&&!stopped){clearTimeout(pollId);poll()}});timerId=setInterval(tick,1000);render('{{.Status}}');tick();poll()})();</script></body></html>`))
