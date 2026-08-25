// Package danfe gera o DANFE (Documento Auxiliar da Nota Fiscal Eletrônica)
// em PDF a partir do XML oficial da NFe/CT-e, replicando o layout retrato
// padrão do MOC (o mesmo que qualquer emissor — Fsist incluso — produz).
package danfe

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// endereco cobre tanto <enderEmit> quanto <enderDest> — mesmo schema.
type endereco struct {
	XLgr    string `xml:"xLgr"`
	Nro     string `xml:"nro"`
	XCpl    string `xml:"xCpl"`
	XBairro string `xml:"xBairro"`
	XMun    string `xml:"xMun"`
	UF      string `xml:"UF"`
	CEP     string `xml:"CEP"`
	Fone    string `xml:"fone"`
}

type icmsGrupo struct {
	Orig  string `xml:"orig"`
	CST   string `xml:"CST"`
	VBC   string `xml:"vBC"`
	PICMS string `xml:"pICMS"`
	VICMS string `xml:"vICMS"`
}

type icms struct {
	ICMS00 *icmsGrupo `xml:"ICMS00"`
	ICMS10 *icmsGrupo `xml:"ICMS10"`
	ICMS20 *icmsGrupo `xml:"ICMS20"`
	ICMS40 *icmsGrupo `xml:"ICMS40"`
	ICMS51 *icmsGrupo `xml:"ICMS51"`
	ICMS60 *icmsGrupo `xml:"ICMS60"`
	ICMS90 *icmsGrupo `xml:"ICMS90"`
}

// grupo retorna o subgrupo de ICMS que veio preenchido — só um existe por vez.
func (i icms) grupo() *icmsGrupo {
	for _, g := range []*icmsGrupo{i.ICMS00, i.ICMS10, i.ICMS20, i.ICMS40, i.ICMS51, i.ICMS60, i.ICMS90} {
		if g != nil {
			return g
		}
	}
	return &icmsGrupo{}
}

type ipiTrib struct {
	CST  string `xml:"CST"`
	VIPI string `xml:"vIPI"`
	PIPI string `xml:"pIPI"`
}

type ipi struct {
	IPITrib *ipiTrib `xml:"IPITrib"`
	IPINT   *struct {
		CST string `xml:"CST"`
	} `xml:"IPINT"`
}

func (i ipi) valores() (cst string, vIPI, pIPI float64) {
	if i.IPITrib != nil {
		vIPI, _ = strconv.ParseFloat(i.IPITrib.VIPI, 64)
		pIPI, _ = strconv.ParseFloat(i.IPITrib.PIPI, 64)
		return i.IPITrib.CST, vIPI, pIPI
	}
	if i.IPINT != nil {
		return i.IPINT.CST, 0, 0
	}
	return "", 0, 0
}

type detItem struct {
	NItem string `xml:"nItem,attr"`
	Prod  struct {
		CProd  string `xml:"cProd"`
		XProd  string `xml:"xProd"`
		NCM    string `xml:"NCM"`
		CFOP   string `xml:"CFOP"`
		UCom   string `xml:"uCom"`
		QCom   string `xml:"qCom"`
		VUnCom string `xml:"vUnCom"`
		VProd  string `xml:"vProd"`
	} `xml:"prod"`
	Imposto struct {
		ICMS icms `xml:"ICMS"`
		IPI  ipi  `xml:"IPI"`
	} `xml:"imposto"`
}

type duplicata struct {
	NDup  string `xml:"nDup"`
	DVenc string `xml:"dVenc"`
	VDup  string `xml:"vDup"`
}

type nfeProcFull struct {
	XMLName xml.Name `xml:"nfeProc"`
	NFe     struct {
		InfNFe struct {
			Id  string `xml:"Id,attr"`
			Ide struct {
				NatOp    string `xml:"natOp"`
				Serie    string `xml:"serie"`
				NNF      string `xml:"nNF"`
				DhEmi    string `xml:"dhEmi"`
				DhSaiEnt string `xml:"dhSaiEnt"`
				TpNF     string `xml:"tpNF"` // 0=entrada, 1=saída
			} `xml:"ide"`
			Emit struct {
				CNPJ      string   `xml:"CNPJ"`
				XNome     string   `xml:"xNome"`
				EnderEmit endereco `xml:"enderEmit"`
				IE        string   `xml:"IE"`
				IM        string   `xml:"IM"`
			} `xml:"emit"`
			Dest struct {
				CNPJ      string   `xml:"CNPJ"`
				XNome     string   `xml:"xNome"`
				EnderDest endereco `xml:"enderDest"`
				IE        string   `xml:"IE"`
				Email     string   `xml:"email"`
			} `xml:"dest"`
			Entrega *struct {
				CNPJ    string `xml:"CNPJ"`
				XNome   string `xml:"xNome"`
				XLgr    string `xml:"xLgr"`
				Nro     string `xml:"nro"`
				XBairro string `xml:"xBairro"`
				XMun    string `xml:"xMun"`
				UF      string `xml:"UF"`
				CEP     string `xml:"CEP"`
				Fone    string `xml:"fone"`
			} `xml:"entrega"`
			Det   []detItem `xml:"det"`
			Total struct {
				ICMSTot struct {
					VBC      string `xml:"vBC"`
					VICMS    string `xml:"vICMS"`
					VBCST    string `xml:"vBCST"`
					VST      string `xml:"vST"`
					VProd    string `xml:"vProd"`
					VFrete   string `xml:"vFrete"`
					VSeg     string `xml:"vSeg"`
					VDesc    string `xml:"vDesc"`
					VII      string `xml:"vII"`
					VIPI     string `xml:"vIPI"`
					VPIS     string `xml:"vPIS"`
					VCOFINS  string `xml:"vCOFINS"`
					VOutro   string `xml:"vOutro"`
					VNF      string `xml:"vNF"`
					VTotTrib string `xml:"vTotTrib"`
				} `xml:"ICMSTot"`
			} `xml:"total"`
			Transp struct {
				ModFrete   string `xml:"modFrete"`
				Transporta struct {
					CNPJ  string `xml:"CNPJ"`
					XNome string `xml:"xNome"`
				} `xml:"transporta"`
				Vol []struct {
					PesoL string `xml:"pesoL"`
					PesoB string `xml:"pesoB"`
				} `xml:"vol"`
			} `xml:"transp"`
			Cobr struct {
				Dup []duplicata `xml:"dup"`
			} `xml:"cobr"`
			InfAdic struct {
				InfCpl string `xml:"infCpl"`
			} `xml:"infAdic"`
		} `xml:"infNFe"`
	} `xml:"NFe"`
	ProtNFe struct {
		InfProt struct {
			ChNFe    string `xml:"chNFe"`
			DhRecbto string `xml:"dhRecbto"`
			NProt    string `xml:"nProt"`
		} `xml:"infProt"`
	} `xml:"protNFe"`
}

// NFe é a struct plana já pronta pro template do DANFE — números convertidos,
// sem precisar o gerador de PDF entender nada de schema XML.
type NFe struct {
	Chave    string
	NNF      string
	Serie    string
	NatOp    string
	TpNF     string // "0" entrada, "1" saída
	DhEmi    time.Time
	DhSaiEnt time.Time
	NProt    string
	DhRecbto time.Time

	EmitNome  string
	EmitCNPJ  string
	EmitEnder endereco
	EmitIE    string
	EmitIM    string

	DestNome  string
	DestCNPJ  string
	DestEnder endereco
	DestIE    string
	DestEmail string

	TemEntrega   bool
	EntregaNome  string
	EntregaCNPJ  string
	EntregaEnder endereco

	Itens []Item

	VBC, VICMS, VBCST, VST, VProd, VFrete, VSeg, VDesc, VII, VIPI, VPIS, VCOFINS, VOutro, VNF, VTotTrib float64

	ModFrete    string
	TranspNome  string
	TranspCNPJ  string
	PesoBruto   float64
	PesoLiquido float64

	Duplicatas []Duplicata

	InfCpl string
}

// Item é uma linha da tabela "DADOS DOS PRODUTOS / SERVIÇOS".
type Item struct {
	CProd, XProd, NCM, CST, CFOP, UCom string
	QCom, VUnCom, VProd, VDesc         float64
	VBCICMS, VICMS, VIPI, PICMS, PIPI  float64
}

// Duplicata é uma linha da tabela "FATURA / DUPLICATA".
type Duplicata struct {
	Numero string
	Venc   time.Time
	Valor  float64
}

func f(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func parseData(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

// ParseNFe lê o XML completo (nfeProc) e devolve a struct plana pronta pro
// gerador de PDF. Diferente de document.ParseNFe (que só pega os 4 campos
// usados pro nome de arquivo), este pega TODOS os campos que aparecem no
// DANFE oficial.
func ParseNFe(raw []byte) (NFe, error) {
	var p nfeProcFull
	if err := xml.Unmarshal(raw, &p); err != nil {
		return NFe{}, fmt.Errorf("parse NFe pro DANFE: %w", err)
	}
	inf := p.NFe.InfNFe

	out := NFe{
		Chave:    strings.TrimPrefix(inf.Id, "NFe"),
		NNF:      inf.Ide.NNF,
		Serie:    inf.Ide.Serie,
		NatOp:    inf.Ide.NatOp,
		TpNF:     inf.Ide.TpNF,
		DhEmi:    parseData(inf.Ide.DhEmi),
		DhSaiEnt: parseData(inf.Ide.DhSaiEnt),
		NProt:    p.ProtNFe.InfProt.NProt,
		DhRecbto: parseData(p.ProtNFe.InfProt.DhRecbto),

		EmitNome:  inf.Emit.XNome,
		EmitCNPJ:  inf.Emit.CNPJ,
		EmitEnder: inf.Emit.EnderEmit,
		EmitIE:    inf.Emit.IE,
		EmitIM:    inf.Emit.IM,

		DestNome:  inf.Dest.XNome,
		DestCNPJ:  inf.Dest.CNPJ,
		DestEnder: inf.Dest.EnderDest,
		DestIE:    inf.Dest.IE,
		DestEmail: inf.Dest.Email,

		VBC:      f(inf.Total.ICMSTot.VBC),
		VICMS:    f(inf.Total.ICMSTot.VICMS),
		VBCST:    f(inf.Total.ICMSTot.VBCST),
		VST:      f(inf.Total.ICMSTot.VST),
		VProd:    f(inf.Total.ICMSTot.VProd),
		VFrete:   f(inf.Total.ICMSTot.VFrete),
		VSeg:     f(inf.Total.ICMSTot.VSeg),
		VDesc:    f(inf.Total.ICMSTot.VDesc),
		VII:      f(inf.Total.ICMSTot.VII),
		VIPI:     f(inf.Total.ICMSTot.VIPI),
		VPIS:     f(inf.Total.ICMSTot.VPIS),
		VCOFINS:  f(inf.Total.ICMSTot.VCOFINS),
		VOutro:   f(inf.Total.ICMSTot.VOutro),
		VNF:      f(inf.Total.ICMSTot.VNF),
		VTotTrib: f(inf.Total.ICMSTot.VTotTrib),

		ModFrete:   inf.Transp.ModFrete,
		TranspNome: inf.Transp.Transporta.XNome,
		TranspCNPJ: inf.Transp.Transporta.CNPJ,

		InfCpl: inf.InfAdic.InfCpl,
	}

	for _, v := range inf.Transp.Vol {
		out.PesoBruto += f(v.PesoB)
		out.PesoLiquido += f(v.PesoL)
	}

	if inf.Entrega != nil {
		out.TemEntrega = true
		out.EntregaNome = inf.Entrega.XNome
		out.EntregaCNPJ = inf.Entrega.CNPJ
		out.EntregaEnder = endereco{
			XLgr:    inf.Entrega.XLgr,
			Nro:     inf.Entrega.Nro,
			XBairro: inf.Entrega.XBairro,
			XMun:    inf.Entrega.XMun,
			UF:      inf.Entrega.UF,
			CEP:     inf.Entrega.CEP,
			Fone:    inf.Entrega.Fone,
		}
	}

	for _, d := range inf.Det {
		g := d.Imposto.ICMS.grupo()
		_, vIPI, pIPI := d.Imposto.IPI.valores()
		out.Itens = append(out.Itens, Item{
			CProd:   d.Prod.CProd,
			XProd:   d.Prod.XProd,
			NCM:     d.Prod.NCM,
			CST:     g.CST,
			CFOP:    d.Prod.CFOP,
			UCom:    d.Prod.UCom,
			QCom:    f(d.Prod.QCom),
			VUnCom:  f(d.Prod.VUnCom),
			VProd:   f(d.Prod.VProd),
			VBCICMS: f(g.VBC),
			VICMS:   f(g.VICMS),
			PICMS:   f(g.PICMS),
			VIPI:    vIPI,
			PIPI:    pIPI,
		})
	}

	for _, d := range inf.Cobr.Dup {
		out.Duplicatas = append(out.Duplicatas, Duplicata{
			Numero: d.NDup,
			Venc:   parseData(d.DVenc),
			Valor:  f(d.VDup),
		})
	}

	return out, nil
}
