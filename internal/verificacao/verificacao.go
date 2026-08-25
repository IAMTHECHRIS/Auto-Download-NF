// Package verificacao compara o catálogo do que já foi baixado com o que
// realmente existe na pasta de destino (a pasta sincronizada de verdade,
// ex: dentro do Google Drive, pra onde o usuário copia manualmente os
// arquivos depois de conferir).
//
// IMPORTANTE: isso é só um checador informativo. Como o fluxo normal é
// COPIAR pra pasta de destino (o original continua na pasta de chegada),
// tudo que aparecer aqui como "faltando" é candidato real a "esqueci de
// copiar" — mas se o usuário mudar de ideia e passar a MOVER em vez de
// copiar, a lista passa a incluir também tudo que foi movido de propósito
// (não tem como o programa saber a diferença só olhando o disco).
package verificacao

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"io-nf-automation/internal/catalogo"
)

// Faltando é uma nota que está no catálogo mas cuja chave de acesso não foi
// encontrada em nenhum XML dentro da pasta de destino.
type Faltando struct {
	Tipo          string    `json:"tipo"`
	Fornecedor    string    `json:"fornecedor"`
	FornecedorDoc string    `json:"fornecedor_doc"`
	Data          time.Time `json:"data"`
	Numero        string    `json:"numero"`
	Valor         float64   `json:"valor"`
	Chave         string    `json:"chave"`
	Caminho       string    `json:"caminho"` // caminho original, na pasta de chegada
}

// reChaveEmXML acha a chave de acesso (44 dígitos) dentro do conteúdo bruto
// de um XML de NFe/CT-e/NFS-e, tentando os dois lugares onde ela costuma
// aparecer — o atributo Id="NFe..." do infNFe, e a tag <chNFe> (usada no
// protocolo de autorização e em alguns resumos).
var reChaveEmXML = regexp.MustCompile(`(?:Id="(?:NFe|CTe)|<ch(?:NFe|CTe|MDFe))(\d{44})`)

// chaveDoArquivo lê um .xml da pasta de destino e extrai a chave de acesso
// de dentro do CONTEÚDO — não do nome do arquivo. É o que faz a comparação
// funcionar mesmo quando o arquivo na pasta foi renomeado por outra pessoa
// ou sistema (bug reportado: comparar só por nome dava falso "faltando"
// pra notas que estavam lá, só que com nome diferente do que o programa
// geraria).
func chaveDoArquivo(caminho string) string {
	if !strings.EqualFold(filepath.Ext(caminho), ".xml") {
		return ""
	}
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return ""
	}
	m := reChaveEmXML.FindSubmatch(dados)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// Escopo é o que Inferir() consegue deduzir do CAMINHO da pasta escolhida
// pra verificar — não do conteúdo dela. Existe pra restringir a comparação
// só ao que faz sentido pra aquela pasta específica, em vez de comparar o
// catálogo inteiro (todos os meses, Entrada e Saída misturados) contra uma
// pasta que só guarda uma fatia disso.
//
// Convenção de pasta esperada (documentada no README pra quem for montar a
// própria estrutura de conferência):
//
//	.../NF ENTRADA/<ANO>/<MM>_<ANO>/<NFEC|NFES>/...
//	.../NF SAÍDA/.../<ANO>/<MM>_<ANO>/<NFSS>/...
//
// Cada nível é opcional — se a pasta escolhida for só ".../2026/07_2026"
// (sem o subnível de tipo), ainda dá pra filtrar por mês+direção, só não
// por tipo exato.
type Escopo struct {
	TemPeriodo bool   `json:"tem_periodo"`
	Ano        int    `json:"ano"`
	Mes        int    `json:"mes"`     // 1-12
	Direcao    string `json:"direcao"` // "entrada", "saida", ou "" se não deu pra saber
	Tipo       string `json:"tipo"`    // TipoNFEC/TipoNFES/"NFES-EMITIDA"/TipoCUPOM/TipoFAT do pacote document, ou "" se não deu pra saber
}

var reMesAno = regexp.MustCompile(`^(0[1-9]|1[0-2])_(\d{4})$`)

// tiposPasta mapeia o nome da SUBPASTA (o que o usuário de fato cria no
// disco) pro Tipo interno do catálogo — nem sempre é o mesmo texto: a saída
// usa "NFSS" na pasta mas o catálogo guarda como "NFES-EMITIDA".
var tiposPasta = map[string]string{
	"nfec":  "NFEC",
	"nfes":  "NFES",
	"nfer":  "NFER", // entrada de remessa — pasta usada na prática, ainda sem coleta automática pra esse tipo
	"nfss":  "NFES-EMITIDA",
	"nfsr":  "NFSR", // saída de remessa — mesma ressalva do NFER
	"cupom": "CUPOM",
	"fat":   "FAT",
}

func semAcento(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch unicode.ToUpper(r) {
		case 'Á', 'À', 'Â', 'Ã':
			r = 'A'
		case 'É', 'Ê':
			r = 'E'
		case 'Í':
			r = 'I'
		case 'Ó', 'Ô', 'Õ':
			r = 'O'
		case 'Ú':
			r = 'U'
		default:
			r = unicode.ToUpper(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Inferir olha cada segmento do caminho da pasta escolhida e tenta
// reconhecer direção (Entrada/Saída), período (MM_AAAA) e tipo
// (NFEC/NFES/NFSS/...) — nessa ordem de generalidade. Segmentos que não
// batem com nada conhecido são ignorados (é normal ter "NOTAS FISCAIS -
// 2026" ou qualquer outro nome livre no meio do caminho).
func Inferir(pastaDestino string) Escopo {
	var esc Escopo
	partes := strings.FieldsFunc(pastaDestino, func(r rune) bool { return r == '/' || r == '\\' })

	for _, parte := range partes {
		limpo := strings.TrimSpace(parte)
		maiuscSemAcento := semAcento(limpo)

		if !esc.TemPeriodo {
			if m := reMesAno.FindStringSubmatch(limpo); m != nil {
				var mes, ano int
				fmt.Sscanf(m[1], "%d", &mes)
				fmt.Sscanf(m[2], "%d", &ano)
				esc.Mes, esc.Ano, esc.TemPeriodo = mes, ano, true
				continue
			}
		}

		if esc.Direcao == "" {
			if strings.Contains(maiuscSemAcento, "ENTRADA") {
				esc.Direcao = "entrada"
				continue
			}
			if strings.Contains(maiuscSemAcento, "SAIDA") {
				esc.Direcao = "saida"
				continue
			}
		}

		if esc.Tipo == "" {
			if tipo, ok := tiposPasta[strings.ToLower(limpo)]; ok {
				esc.Tipo = tipo
			}
		}
	}

	return esc
}

// Verificar varre pastaDestino (recursivamente) coletando todo nome de
// arquivo presente, e devolve as entradas do catálogo cujo nome de arquivo
// não apareceu em lugar nenhum lá dentro. Comparação é só pelo NOME do
// arquivo (não pelo conteúdo) — funciona bem porque o nome já é
// determinístico (fornecedor+data+tipo+número+valor).
// Resultado junta o que Verificar() apura — o campo TotalNoEscopo existe
// pra distinguir dois casos que pareciam a mesma coisa antes ("faltando"
// vazio): TotalNoEscopo=0 significa que o CATÁLOGO não tem nenhuma nota
// daquele recorte (mês/tipo/direção) pra comparar — não dá pra confirmar
// nada, é diferente de "comparei e está tudo lá".
type Resultado struct {
	Escopo        Escopo
	TotalNoEscopo int
	Faltando      []Faltando
}

func Verificar(pastaSaida, pastaDestino string) (Resultado, error) {
	if strings.TrimSpace(pastaDestino) == "" {
		return Resultado{}, fmt.Errorf("pasta de destino não configurada — defina ela na aba Configuração antes de verificar")
	}

	entradas, err := catalogo.Listar(pastaSaida)
	if err != nil {
		return Resultado{}, fmt.Errorf("ler catálogo: %w", err)
	}

	esc := Inferir(pastaDestino)
	entradas = filtrarPorEscopo(entradas, esc)

	// Duas fontes de "presente": a CHAVE (lida de dentro de cada .xml —
	// critério principal, funciona não importa como o arquivo foi nomeado)
	// e o NOME do arquivo (fallback, útil pra PDF/outros formatos sem chave
	// fácil de extrair, ou XML que por algum motivo não deu pra ler).
	presentesChave := make(map[string]bool)
	presentesNome := make(map[string]bool)
	err = filepath.WalkDir(pastaDestino, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			// pasta/arquivo pontual sem permissão de leitura, por exemplo —
			// pula e continua, não aborta a verificação inteira por isso.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		presentesNome[strings.ToLower(d.Name())] = true
		if chave := chaveDoArquivo(caminho); chave != "" {
			presentesChave[chave] = true
		}
		return nil
	})
	if err != nil {
		return Resultado{Escopo: esc}, fmt.Errorf("varrer pasta de destino: %w", err)
	}

	var faltando []Faltando
	totalNoEscopo := 0
	for _, e := range entradas {
		// notas já marcadas CANCELADO não interessam pro controle de "cópia
		// pendente" — não faz sentido cobrar cópia de algo cancelado, e não
		// contam como "base de comparação" real também.
		if strings.EqualFold(e.Status, "CANCELADO") {
			continue
		}
		totalNoEscopo++

		achou := e.Chave != "" && presentesChave[e.Chave]
		if !achou {
			nome := strings.ToLower(filepath.Base(e.Caminho))
			achou = presentesNome[nome]
		}
		if achou {
			continue
		}

		faltando = append(faltando, Faltando{
			Tipo:          e.Tipo,
			Fornecedor:    e.Fornecedor,
			FornecedorDoc: e.FornecedorDoc,
			Data:          e.Data,
			Numero:        e.Numero,
			Valor:         e.Valor,
			Chave:         e.Chave,
			Caminho:       e.Caminho,
		})
	}

	sort.Slice(faltando, func(i, j int) bool {
		return faltando[i].Data.After(faltando[j].Data)
	})

	return Resultado{Escopo: esc, TotalNoEscopo: totalNoEscopo, Faltando: faltando}, nil
}

// filtrarPorEscopo restringe as entradas do catálogo ao que o Escopo
// inferido da pasta escolhida realmente cobre — sem isso, comparar contra
// uma pasta de um mês só faz TUDO que não é daquele mês aparecer como
// "faltando", quando na verdade nem deveria estar ali.
func filtrarPorEscopo(entradas []catalogo.Entrada, esc Escopo) []catalogo.Entrada {
	if !esc.TemPeriodo && esc.Direcao == "" && esc.Tipo == "" {
		return entradas
	}
	var out []catalogo.Entrada
	for _, e := range entradas {
		if esc.TemPeriodo && (int(e.Data.Month()) != esc.Mes || e.Data.Year() != esc.Ano) {
			continue
		}
		ehSaida := e.Tipo == "NFES-EMITIDA"
		if esc.Direcao == "entrada" && ehSaida {
			continue
		}
		if esc.Direcao == "saida" && !ehSaida {
			continue
		}
		if esc.Tipo != "" && e.Tipo != esc.Tipo {
			continue
		}
		out = append(out, e)
	}
	return out
}
