package danfe

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/jung-kurt/gofpdf"
)

const (
	pageW    = 210.0
	pageH    = 297.0
	margin   = 5.0
	contentW = pageW - 2*margin
)

// Gerar escreve o DANFE em PDF (layout retrato, modelo padrão do MOC) no
// writer informado. gerador é o texto que aparece no rodapé no lugar de
// "Gerado em www.fsist.com.br".
func Gerar(w io.Writer, nfe NFe, gerador string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(false, margin)
	pdf.AddPage()
	pdf.SetFont("Times", "", 7)
	tr = pdf.UnicodeTranslatorFromDescriptor("")

	y := margin
	y = drawCanhoto(pdf, nfe, y)
	y = drawCabecalho(pdf, nfe, y)
	y = drawNaturezaProtocolo(pdf, nfe, y)
	y = drawInscricoes(pdf, nfe, y)
	y = drawPessoa(pdf, "DESTINATÁRIO / REMETENTE", nfe.DestNome, nfe.DestCNPJ, nfe.DestEnder, nfe.DestIE, nfe.DhEmi, nfe.DhSaiEnt, y, true)
	if nfe.TemEntrega {
		y = drawPessoa(pdf, "INFORMAÇÕES DO LOCAL DE ENTREGA", nfe.EntregaNome, nfe.EntregaCNPJ, nfe.EntregaEnder, "", time.Time{}, time.Time{}, y, false)
	}
	y = drawFatura(pdf, nfe, y)
	y = drawImpostos(pdf, nfe, y)
	y = drawTransportador(pdf, nfe, y)
	y = drawProdutos(pdf, nfe, y)
	drawAdicionais(pdf, nfe, y, gerador)

	return pdf.Output(w)
}

// --- helpers de baixo nível ---

// tr converte UTF-8 pra cp1252 (WinAnsi) — as fontes core do gofpdf
// (Helvetica) não entendem UTF-8 direto, senão "Ç"/"Ã"/"É" viram lixo.
// Setado uma vez em Gerar via pdf.UnicodeTranslatorFromDescriptor("").
var tr = func(s string) string { return s }

func cellF(pdf *gofpdf.Fpdf, w, h float64, txt, border string, ln int, align string, fill bool, link int, linkStr string) {
	pdf.CellFormat(w, h, tr(txt), border, ln, align, fill, link, linkStr)
}

func multiF(pdf *gofpdf.Fpdf, w, h float64, txt, border, align string, fill bool) {
	pdf.MultiCell(w, h, tr(txt), border, align, fill)
}

func box(pdf *gofpdf.Fpdf, x, y, w, h float64) {
	pdf.Rect(x, y, w, h, "D")
}

// truncF corta o texto (com "…" no fim) até caber em w mm com a fonte atual
// — o gofpdf não faz clip de célula sozinho, texto comprido simplesmente
// desenha por cima da coluna vizinha se não cortarmos antes.
func truncF(pdf *gofpdf.Fpdf, s string, w float64) string {
	if pdf.GetStringWidth(tr(s)) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + "…"
		if pdf.GetStringWidth(tr(candidate)) <= w {
			return candidate
		}
	}
	return ""
}

// campo desenha uma caixinha com rótulo pequeno no topo e valor abaixo —
// o padrão visual usado em quase todo o DANFE (ex: "CNPJ/CPF" + número).
func campo(pdf *gofpdf.Fpdf, x, y, w, h float64, label, valor string, valorSize float64, bold bool) {
	box(pdf, x, y, w, h)
	pdf.SetFont("Times", "", 5)
	pdf.SetXY(x+0.8, y+0.6)
	cellF(pdf, w-1.6, 2.2, label, "", 0, "L", false, 0, "")
	style := ""
	if bold {
		style = "B"
	}
	pdf.SetFont("Times", style, valorSize)
	pdf.SetXY(x+0.8, y+3)
	cellF(pdf, w-1.6, h-3.4, valor, "", 0, "L", false, 0, "")
}

func moeda(v float64) string {
	return fmt.Sprintf("%s", formatBR(v))
}

func formatBR(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.2f", v)
	intPart := s[:len(s)-3]
	decPart := s[len(s)-2:]
	// separador de milhar
	var out []byte
	for i, c := range []byte(intPart) {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	res := string(out) + "," + decPart
	if neg {
		res = "-" + res
	}
	return res
}

func dataBR(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("02/01/2006")
}

func horaBR(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

func chaveFormatada(chave string) string {
	var out string
	for i := 0; i < len(chave); i += 4 {
		end := i + 4
		if end > len(chave) {
			end = len(chave)
		}
		if i > 0 {
			out += " "
		}
		out += chave[i:end]
	}
	return out
}

// --- seções ---

func drawCanhoto(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	h := 15.0
	boxW := contentW - 30
	box(pdf, margin, y, boxW, h)
	pdf.SetFont("Times", "", 6.5)
	txt := fmt.Sprintf(
		"RECEBEMOS DE %s OS PRODUTOS E/OU SERVIÇOS CONSTANTES DA NOTA FISCAL ELETRÔNICA INDICADA ABAIXO. "+
			"EMISSÃO: %s VALOR TOTAL: R$ %s DESTINATÁRIO: %s - %s, %s %s %s-%s",
		nfe.EmitNome, dataBR(nfe.DhEmi), moeda(nfe.VNF), nfe.DestNome,
		nfe.DestEnder.XLgr, nfe.DestEnder.Nro, nfe.DestEnder.XBairro, nfe.DestEnder.XMun, nfe.DestEnder.UF)
	pdf.SetFont("Times", "", 6)
	pdf.SetXY(margin+1, y+0.8)
	multiF(pdf, boxW-2, 2.3, txt, "", "L", false)

	// linha divisória + duas colunas inferiores
	midY := y + 8.5
	pdf.Line(margin, midY, margin+boxW, midY)
	pdf.Line(margin+boxW*0.35, midY, margin+boxW*0.35, y+h)
	pdf.SetFont("Times", "", 5.5)
	pdf.SetXY(margin+1, midY+1)
	cellF(pdf, boxW*0.35-2, 3, "DATA DE RECEBIMENTO", "", 0, "L", false, 0, "")
	pdf.SetXY(margin+boxW*0.35+1, midY+1)
	cellF(pdf, boxW*0.65-2, 3, "IDENTIFICAÇÃO E ASSINATURA DO RECEBEDOR", "", 0, "L", false, 0, "")

	// caixa nº/série à direita
	x2 := margin + boxW
	w2 := contentW - boxW
	box(pdf, x2, y, w2, h)
	pdf.SetFont("Times", "B", 10)
	pdf.SetXY(x2, y+1)
	cellF(pdf, w2, 5, "NF-e", "", 0, "C", false, 0, "")
	pdf.SetFont("Times", "", 7)
	pdf.SetXY(x2, y+6)
	cellF(pdf, w2, 3.5, fmt.Sprintf("Nº. %s", numeroFormatado(nfe.NNF)), "", 0, "C", false, 0, "")
	pdf.SetXY(x2, y+9.3)
	cellF(pdf, w2, 3.5, fmt.Sprintf("Série %s", nfe.Serie), "", 0, "C", false, 0, "")

	pdf.Line(margin, y+h, margin+contentW, y+h)
	return y + h + 1
}

func numeroFormatado(nnf string) string {
	// zero-pad até 9 dígitos e agrupa de 3 em 3, ex: "262220" -> "000.262.220"
	for len(nnf) < 9 {
		nnf = "0" + nnf
	}
	return nnf[0:3] + "." + nnf[3:6] + "." + nnf[6:9]
}

func drawCabecalho(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	h := 26.0
	col1 := contentW * 0.42
	col2 := contentW * 0.20
	col3 := contentW - col1 - col2

	x1, x2, x3 := margin, margin+col1, margin+col1+col2
	box(pdf, x1, y, col1, h)
	box(pdf, x2, y, col2, h)
	box(pdf, x3, y, col3, h)

	// coluna 1: emitente
	pdf.SetFont("Times", "", 5)
	pdf.SetXY(x1+1, y+0.8)
	cellF(pdf, col1-2, 2.2, "IDENTIFICAÇÃO DO EMITENTE", "", 0, "C", false, 0, "")
	pdf.SetFont("Times", "B", 7.5)
	pdf.SetXY(x1+2, y+5.5)
	multiF(pdf, col1-4, 2.8, nfe.EmitNome, "", "L", false)
	nomeLinhas := len(pdf.SplitLines([]byte(tr(nfe.EmitNome)), col1-4))
	if nomeLinhas < 1 {
		nomeLinhas = 1
	}
	pdf.SetFont("Times", "", 6.5)
	end := nfe.EmitEnder
	linhas := []string{
		fmt.Sprintf("%s, %s", end.XLgr, end.Nro),
		fmt.Sprintf("%s - %s", end.XBairro, cepFmt(end.CEP)),
		fmt.Sprintf("%s - %s   Fone/Fax: %s", end.XMun, end.UF, end.Fone),
	}
	yy := y + 5.5 + float64(nomeLinhas)*2.8 + 0.5
	for _, l := range linhas {
		pdf.SetXY(x1+2, yy)
		cellF(pdf, col1-4, 3, l, "", 0, "L", false, 0, "")
		yy += 3.0
	}

	// coluna 2: DANFE + entrada/saída
	pdf.SetFont("Times", "B", 12)
	pdf.SetXY(x2, y+2)
	cellF(pdf, col2, 5, "DANFE", "", 0, "C", false, 0, "")
	pdf.SetFont("Times", "", 5.5)
	pdf.SetXY(x2+2, y+6.5)
	multiF(pdf, col2-4, 2.4, "Documento Auxiliar da Nota Fiscal Eletrônica", "", "C", false)
	pdf.SetFont("Times", "", 6)
	pdf.SetXY(x2+2, y+12)
	multiF(pdf, col2-4-6, 3, "0 - ENTRADA\n1 - SAÍDA", "", "L", false)
	tipoBox := 5.0
	tx := x2 + col2 - tipoBox - 2
	ty := y + 12
	box(pdf, tx, ty, tipoBox, tipoBox)
	pdf.SetFont("Times", "B", 10)
	pdf.SetXY(tx, ty+0.5)
	cellF(pdf, tipoBox, tipoBox-1, nfe.TpNF, "", 0, "C", false, 0, "")
	pdf.SetFont("Times", "", 6.5)
	pdf.SetXY(x2, y+19)
	cellF(pdf, col2, 3, fmt.Sprintf("Nº. %s", numeroFormatado(nfe.NNF)), "", 0, "C", false, 0, "")
	pdf.SetXY(x2, y+22.3)
	cellF(pdf, col2, 3, fmt.Sprintf("Série %s   Folha 1/1", nfe.Serie), "", 0, "C", false, 0, "")

	// coluna 3: código de barras + chave
	if img, w, h2, err := barcodeImage(nfe.Chave); err == nil {
		name := "danfe-barcode"
		pdf.RegisterImageOptionsReader(name, gofpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(img))
		bw := col3 - 4
		bh := bw * h2 / w
		if bh > 8 {
			bh = 8
			bw = bh * w / h2
		}
		pdf.ImageOptions(name, x3+(col3-bw)/2, y+2, bw, bh, false, gofpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	}
	pdf.SetFont("Times", "", 5)
	pdf.SetXY(x3+1, y+11)
	cellF(pdf, col3-2, 2.2, "CHAVE DE ACESSO", "", 0, "L", false, 0, "")
	pdf.SetFont("Times", "", 6.5)
	pdf.SetXY(x3+1, y+14.3)
	multiF(pdf, col3-2, 3, chaveFormatada(nfe.Chave), "", "C", false)
	pdf.SetFont("Times", "", 5)
	pdf.SetXY(x3+1, y+19.5)
	multiF(pdf, col3-2, 2.2, "Consulta de autenticidade no portal nacional da NF-e www.nfe.fazenda.gov.br/portal ou no site da Sefaz Autorizadora", "", "C", false)

	return y + h + 1
}

func cepFmt(cep string) string {
	if len(cep) != 8 {
		return cep
	}
	return cep[0:5] + "-" + cep[5:]
}

func cnpjFmt(c string) string {
	if len(c) != 14 {
		return c
	}
	return fmt.Sprintf("%s.%s.%s/%s-%s", c[0:2], c[2:5], c[5:8], c[8:12], c[12:])
}

func barcodeImage(chave string) ([]byte, float64, float64, error) {
	bc, err := code128.Encode(chave)
	if err != nil {
		return nil, 0, 0, err
	}
	scaled, err := barcode.Scale(bc, 600, 100)
	if err != nil {
		return nil, 0, 0, err
	}
	// barcode.Scale devolve image.Gray16 — o decoder PNG do gofpdf só lida
	// com 8-bit, então converte pra image.Gray antes de codificar.
	bounds := scaled.Bounds()
	gray8 := image.NewGray(bounds)
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for px := bounds.Min.X; px < bounds.Max.X; px++ {
			gray8.Set(px, py, color.GrayModel.Convert(scaled.At(px, py)))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, gray8); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), 600, 100, nil
}

func drawNaturezaProtocolo(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	h := 6.5
	w1 := contentW * 0.6
	w2 := contentW - w1
	campo(pdf, margin, y, w1, h, "NATUREZA DA OPERAÇÃO", nfe.NatOp, 7.5, true)
	protocolo := ""
	if nfe.NProt != "" {
		protocolo = fmt.Sprintf("%s - %s %s", nfe.NProt, dataBR(nfe.DhRecbto), horaBR(nfe.DhRecbto))
	}
	campo(pdf, margin+w1, y, w2, h, "PROTOCOLO DE AUTORIZAÇÃO DE USO", protocolo, 6.5, false)
	return y + h + 0.5
}

func drawInscricoes(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	h := 6.5
	ws := []float64{contentW * 0.28, contentW * 0.24, contentW * 0.24, contentW * 0.24}
	x := margin
	campo(pdf, x, y, ws[0], h, "INSCRIÇÃO ESTADUAL", nfe.EmitIE, 7, false)
	x += ws[0]
	campo(pdf, x, y, ws[1], h, "INSCRIÇÃO MUNICIPAL", nfe.EmitIM, 7, false)
	x += ws[1]
	campo(pdf, x, y, ws[2], h, "INSCRIÇÃO ESTADUAL DO SUBST. TRIBUT.", "", 7, false)
	x += ws[2]
	campo(pdf, x, y, ws[3], h, "CNPJ / CPF", cnpjFmt(nfe.EmitCNPJ), 7, false)
	return y + h + 0.5
}

func drawPessoa(pdf *gofpdf.Fpdf, titulo, nome, cnpj string, end endereco, ie string, dhEmi, dhSai time.Time, y float64, comDatas bool) float64 {
	tituloH := 3.0
	rowH := 6.2
	h := tituloH + rowH*3
	box(pdf, margin, y, contentW, h)
	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, y+0.6)
	cellF(pdf, contentW-2, 2.6, titulo, "", 0, "L", false, 0, "")

	row1H := rowH
	rowY := y + tituloH
	nomeW := contentW * 0.55
	cnpjW := contentW * 0.2
	dataW := contentW - nomeW - cnpjW
	campo(pdf, margin, rowY, nomeW, row1H, "NOME / RAZÃO SOCIAL", nome, 7, false)
	campo(pdf, margin+nomeW, rowY, cnpjW, row1H, "CNPJ / CPF", cnpjFmt(cnpj), 7, false)
	dataLabel, dataVal := "", ""
	if comDatas {
		dataLabel, dataVal = "DATA DA EMISSÃO", dataBR(dhEmi)
	}
	campo(pdf, margin+nomeW+cnpjW, rowY, dataW, row1H, dataLabel, dataVal, 7, false)

	rowY += row1H
	row2H := rowH
	enderW := contentW * 0.4
	bairroW := contentW * 0.2
	cepW := contentW * 0.15
	restoW := contentW - enderW - bairroW - cepW
	campo(pdf, margin, rowY, enderW, row2H, "ENDEREÇO", fmt.Sprintf("%s, %s", end.XLgr, end.Nro), 6.5, false)
	campo(pdf, margin+enderW, rowY, bairroW, row2H, "BAIRRO / DISTRITO", end.XBairro, 6.5, false)
	campo(pdf, margin+enderW+bairroW, rowY, cepW, row2H, "CEP", cepFmt(end.CEP), 6.5, false)
	dataLabel2, dataVal2 := "", ""
	if comDatas {
		dataLabel2, dataVal2 = "DATA DA SAÍDA/ENTRADA", dataBR(dhSai)
	}
	campo(pdf, margin+enderW+bairroW+cepW, rowY, restoW, row2H, dataLabel2, dataVal2, 6.5, false)

	rowY += row2H
	munW := contentW * 0.4
	ufW := contentW * 0.1
	foneW := contentW * 0.2
	ieW := contentW * 0.15
	horaW := contentW - munW - ufW - foneW - ieW
	campo(pdf, margin, rowY, munW, rowH, "MUNICÍPIO", end.XMun, 6.5, false)
	campo(pdf, margin+munW, rowY, ufW, rowH, "UF", end.UF, 6.5, false)
	campo(pdf, margin+munW+ufW, rowY, foneW, rowH, "FONE / FAX", end.Fone, 6.5, false)
	campo(pdf, margin+munW+ufW+foneW, rowY, ieW, rowH, "INSCRIÇÃO ESTADUAL", ie, 6.5, false)
	horaLabel, horaVal := "", ""
	if comDatas {
		horaLabel, horaVal = "HORA DA SAÍDA/ENTRADA", horaBR(dhSai)
	}
	campo(pdf, margin+munW+ufW+foneW+ieW, rowY, horaW, rowH, horaLabel, horaVal, 6.5, false)

	return y + h + 1
}

func drawFatura(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	if len(nfe.Duplicatas) == 0 {
		return y
	}
	const perRow = 4
	colW := contentW / perRow
	rows := (len(nfe.Duplicatas) + perRow - 1) / perRow
	tituloH := 3.4
	rowH := 3.6
	h := tituloH + float64(rows)*rowH + 0.5
	box(pdf, margin, y, contentW, h)
	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, y+0.6)
	cellF(pdf, contentW-2, 2.6, "FATURA / DUPLICATA", "", 0, "L", false, 0, "")
	pdf.SetFont("Times", "", 6)
	for i, d := range nfe.Duplicatas {
		row := i / perRow
		col := i % perRow
		x := margin + float64(col)*colW
		yy := y + tituloH + float64(row)*rowH
		pdf.SetXY(x+1, yy+0.3)
		cellF(pdf, colW-2, rowH-0.6, fmt.Sprintf("Núm. %s  Venc. %s  R$ %s", d.Numero, dataBR(d.Venc), moeda(d.Valor)), "", 0, "L", false, 0, "")
	}
	return y + h + 1
}

func drawImpostos(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	h := 12.0
	box(pdf, margin, y, contentW, h)
	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, y+0.6)
	cellF(pdf, contentW-2, 2.6, "CÁLCULO DO IMPOSTO", "", 0, "L", false, 0, "")

	row1 := [][2]string{
		{"BASE DE CÁLC. DO ICMS", moeda(nfe.VBC)},
		{"VALOR DO ICMS", moeda(nfe.VICMS)},
		{"BASE DE CÁLC. ICMS S.T.", moeda(nfe.VBCST)},
		{"VALOR DO ICMS SUBST.", moeda(nfe.VST)},
		{"VALOR DO IPI", moeda(nfe.VIPI)},
		{"V. TOTAL PRODUTOS", moeda(nfe.VProd)},
	}
	row2 := [][2]string{
		{"VALOR DO FRETE", moeda(nfe.VFrete)},
		{"VALOR DO SEGURO", moeda(nfe.VSeg)},
		{"DESCONTO", moeda(nfe.VDesc)},
		{"OUTRAS DESPESAS", moeda(nfe.VOutro)},
		{"V. TOT. TRIB.", moeda(nfe.VTotTrib)},
		{"V. TOTAL DA NOTA", moeda(nfe.VNF)},
	}
	colW := contentW / 6
	rowH := (h - 3) / 2
	for i, c := range row1 {
		campo(pdf, margin+float64(i)*colW, y+3, colW, rowH, c[0], c[1], 6.5, false)
	}
	for i, c := range row2 {
		campo(pdf, margin+float64(i)*colW, y+3+rowH, colW, rowH, c[0], c[1], 6.5, false)
	}
	return y + h + 1
}

func drawTransportador(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	tituloH := 3.0
	rowH := 5.6
	h := tituloH + rowH*2
	box(pdf, margin, y, contentW, h)
	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, y+0.6)
	cellF(pdf, contentW-2, 2.6, "TRANSPORTADOR / VOLUMES TRANSPORTADOS", "", 0, "L", false, 0, "")

	freteDesc := map[string]string{
		"0": "0-Emitente", "1": "1-Destinatário", "2": "2-Terceiros",
		"3": "3-Próprio por conta do Rem", "4": "4-Próprio por conta do Dest", "9": "9-Sem frete",
	}[nfe.ModFrete]

	nomeW := contentW * 0.5
	freteW := contentW * 0.2
	cnpjW := contentW - nomeW - freteW
	rowY := y + tituloH
	campo(pdf, margin, rowY, nomeW, rowH, "NOME / RAZÃO SOCIAL", nfe.TranspNome, 6.5, false)
	campo(pdf, margin+nomeW, rowY, freteW, rowH, "FRETE", freteDesc, 6.5, false)
	campo(pdf, margin+nomeW+freteW, rowY, cnpjW, rowH, "CNPJ / CPF", cnpjFmt(nfe.TranspCNPJ), 6.5, false)

	rowY += rowH
	pesoBrutoW := contentW * 0.75
	campo(pdf, margin, rowY, pesoBrutoW, rowH, "PESO BRUTO", formatBR(nfe.PesoBruto), 6.5, false)
	campo(pdf, margin+pesoBrutoW, rowY, contentW-pesoBrutoW, rowH, "PESO LÍQUIDO", formatBR(nfe.PesoLiquido), 6.5, false)

	return y + h + 1
}

func drawProdutos(pdf *gofpdf.Fpdf, nfe NFe, y float64) float64 {
	tituloH := 3.0
	headerRowH := 3.2
	rowH := 3.4
	maxRowsFit := int((pageH - margin - 15 - y - tituloH - headerRowH) / rowH)
	nRows := len(nfe.Itens)
	if nRows > maxRowsFit {
		nRows = maxRowsFit
	}
	h := tituloH + headerRowH + float64(nRows)*rowH
	box(pdf, margin, y, contentW, h)
	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, y+0.6)
	cellF(pdf, contentW-2, 2.6, "DADOS DOS PRODUTOS / SERVIÇOS", "", 0, "L", false, 0, "")

	cols := []struct {
		title string
		w     float64
	}{
		{"CÓDIGO", 0.07}, {"DESCRIÇÃO", 0.20}, {"NCM/SH", 0.055}, {"CST", 0.035},
		{"CFOP", 0.035}, {"UN", 0.03}, {"QUANT", 0.07}, {"V.UNIT", 0.07},
		{"V.TOTAL", 0.075}, {"B.CÁLC ICMS", 0.075}, {"V.ICMS", 0.065}, {"V.IPI", 0.065},
		{"ALÍQ ICMS", 0.06}, {"ALÍQ IPI", 0.06},
	}
	headerY := y + tituloH
	pdf.SetFont("Times", "", 5)
	x := margin
	for _, c := range cols {
		w := c.w * contentW
		pdf.Rect(x, headerY, w, headerRowH, "D")
		pdf.SetXY(x+0.3, headerY+0.2)
		cellF(pdf, w-0.6, headerRowH-0.4, c.title, "", 0, "L", false, 0, "")
		x += w
	}

	// linhas verticais de grade — contínuas do topo do cabeçalho até a
	// última linha de item, separando cada coluna (não só o cabeçalho).
	x = margin
	for _, c := range cols {
		pdf.Line(x, headerY, x, y+h)
		x += c.w * contentW
	}
	pdf.Line(margin+contentW, headerY, margin+contentW, y+h)

	rowY := headerY + headerRowH
	pdf.SetFont("Times", "", 5.2)
	pdf.SetDashPattern([]float64{0.4, 0.4}, 0)
	for i, it := range nfe.Itens {
		if i >= nRows {
			break
		}
		vals := []string{
			it.CProd, it.XProd, it.NCM, it.CST, it.CFOP, it.UCom,
			formatBR(it.QCom), formatBR(it.VUnCom), formatBR(it.VProd),
			formatBR(it.VBCICMS), formatBR(it.VICMS), formatBR(it.VIPI), formatBR(it.PICMS), formatBR(it.PIPI),
		}
		x = margin
		for j, c := range cols {
			w := c.w * contentW
			pdf.SetXY(x+0.3, rowY+0.2)
			align := "L"
			if j >= 6 {
				align = "R"
			}
			cellF(pdf, w-0.6, rowH-0.4, truncF(pdf, vals[j], w-0.6), "", 0, align, false, 0, "")
			x += w
		}
		pdf.Line(margin, rowY+rowH, margin+contentW, rowY+rowH)
		rowY += rowH
	}
	pdf.SetDashPattern([]float64{}, 0)

	return y + h + 1
}

// informacoesComplementares monta o texto de "INFORMAÇÕES COMPLEMENTARES" no
// mesmo formato do DANFE oficial: cada segmento do <infCpl> (separado por
// ";" no XML) numa linha própria, prefixado "Inf. Contribuinte:", mais o
// e-mail do destinatário (se houver) colado na última linha e a divulgação
// obrigatória por lei do valor aproximado dos tributos (Lei 12.741/2012) —
// nenhum dos dois vem pronto no XML, é o gerador do DANFE que acrescenta.
func informacoesComplementares(nfe NFe) string {
	var linhas []string
	partes := strings.Split(nfe.InfCpl, ";")
	for i, p := range partes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i == 0 {
			p = "Inf. Contribuinte: " + p
		}
		linhas = append(linhas, p)
	}
	if nfe.DestEmail != "" && len(linhas) > 0 {
		linhas[len(linhas)-1] += " Email do Destinatário: " + nfe.DestEmail
	} else if nfe.DestEmail != "" {
		linhas = append(linhas, "Email do Destinatário: "+nfe.DestEmail)
	}
	linhas = append(linhas, fmt.Sprintf("Valor Aproximado dos Tributos : R$ %s", moeda(nfe.VTotTrib)))
	return strings.Join(linhas, "\n")
}

func drawAdicionais(pdf *gofpdf.Fpdf, nfe NFe, y float64, gerador string) {
	h := pageH - margin - y
	if h < 15 {
		h = 15
	}
	box(pdf, margin, y, contentW, h)

	// No formulário padrão o bloco "DADOS ADICIONAIS" não começa colado no
	// topo da caixa — sobra um respiro em branco acima dele (o tanto que
	// sobrou da página depois da tabela de produtos). Empurra o conteúdo
	// pra baixo proporcionalmente ao espaço livre, sempre deixando pelo
	// menos ~26mm pro texto + rodapé.
	contentH := 34.0
	cy := y + h - contentH
	minCy := y + h*0.35
	if cy < minCy {
		cy = minCy
	}
	if cy < y {
		cy = y
	}

	pdf.SetFont("Times", "B", 6.5)
	pdf.SetXY(margin+1, cy+0.6)
	cellF(pdf, contentW-2, 2.6, "DADOS ADICIONAIS", "", 0, "L", false, 0, "")
	pdf.Line(margin, cy+3.4, margin+contentW, cy+3.4)

	compW := contentW * 0.7
	pdf.Line(margin+compW, cy+3.4, margin+compW, y+h)
	pdf.SetFont("Times", "", 5)
	pdf.SetXY(margin+1, cy+3.6)
	cellF(pdf, compW-2, 2.2, "INFORMAÇÕES COMPLEMENTARES", "", 0, "L", false, 0, "")
	pdf.SetFont("Times", "", 6)
	pdf.SetXY(margin+1, cy+6)
	multiF(pdf, compW-2, 2.8, informacoesComplementares(nfe), "", "L", false)

	pdf.SetFont("Times", "", 5)
	pdf.SetXY(margin+compW+1, cy+3.6)
	cellF(pdf, contentW-compW-2, 2.2, "RESERVADO AO FISCO", "", 0, "L", false, 0, "")

	pdf.SetFont("Times", "", 5.5)
	pdf.SetXY(margin, pageH-margin-3)
	cellF(pdf, contentW, 3, fmt.Sprintf("Impresso em %s as %s   Gerado por %s", dataBR(time.Now()), horaBR(time.Now()), gerador), "", 0, "L", false, 0, "")
}
