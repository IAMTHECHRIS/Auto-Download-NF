package adn

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuscarLoteAceita404SemDocumento(t *testing.T) {
	var caminho string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.String()
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"StatusProcessamento":"NENHUM_DOCUMENTO_LOCALIZADO","LoteDFe":[],"Alertas":[],"Erros":[{"Codigo":"E2220"}],"TipoAmbiente":"PRODUCAO"}`)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	resp, err := client.BuscarLote(123)
	if err != nil {
		t.Fatalf("BuscarLote() erro = %v", err)
	}
	if resp.StatusProcessamento != "NENHUM_DOCUMENTO_LOCALIZADO" {
		t.Fatalf("StatusProcessamento = %q", resp.StatusProcessamento)
	}
	if caminho != "/DFe/123?lote=true&tipoNSU=DISTRIBUICAO" {
		t.Fatalf("caminho = %q", caminho)
	}
}
