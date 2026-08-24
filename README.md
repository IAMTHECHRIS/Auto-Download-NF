# sieg-automation

Captura, extração e organização automática de documentos fiscais brasileiros
(NFe de compra, NFSe, CT-e) direto dos webservices **oficiais e gratuitos**
da SEFAZ/Receita Federal — sem depender de serviços pagos de terceiros
(SIEG, Focus NFe, etc.) pra esse tipo de dado.

Escrito em Go, roda como um executável único, cross-platform (Linux/Windows),
e se auto-agenda no Windows (Agendador de Tarefas) na primeira execução.

## Por quê

A maioria das ferramentas de "baixar XML de nota fiscal" no mercado é paga,
mesmo quando o dado em si já é acessível de graça pelos webservices oficiais
do governo — a autenticação é feita pelo mesmo certificado digital A1/A3 que
qualquer empresa brasileira já possui. Este projeto nasceu de descobrir isso
na prática e decidir não pagar por algo que o próprio governo já oferece.

## O que cobre hoje

| Fonte | Webservice | Cobre |
|---|---|---|
| NFe de compra / CT-e | `NFeDistribuicaoDFe` (Ambiente Nacional) | Notas onde seu CNPJ é destinatário/transportador |
| NFS-e (nota de serviço) | Sistema Nacional NFS-e (ADN) | Recebidas, emitidas e cancelamentos |

Ambos usam **mTLS com o mesmo certificado A1** — nenhum token, nenhuma API
key paga, nenhum cadastro em serviço terceiro.

**Fora de escopo (por decisão de projeto, não limitação técnica):** NFC-e
(cupom fiscal) não tem um feed de distribuição nacional único — cada estado
trata de forma diferente, e pro volume baixo de cupom não compensou construir
27 integrações estaduais.

## Como funciona

1. Consulta o webservice a partir do último NSU processado (nunca do zero —
   ver nota sobre rate-limit abaixo)
2. Decodifica o XML (base64 + gzip nos dois webservices)
3. Extrai fornecedor, data, número, valor
4. Salva em `ANO/MES/TIPO/FORNECEDOR_DATA_TIPO_NUMERO_VALOR.xml`
5. Cancelamentos são detectados e o arquivo original é renomeado no lugar
   com uma tag de status — não cria pasta separada

## Uso

Na primeira execução, o programa pergunta (e salva num `config.json` local,
nunca commitado):
- CNPJ da empresa
- Caminho do certificado `.pfx` e senha
- Pasta de saída — **de propósito uma pasta LOCAL, fora de qualquer sync de
  nuvem** (OneDrive/Google Drive/Dropbox). É uma área de chegada: você
  confere o que baixou antes de mover pra onde quer que fique sincronizado.

Nas execuções seguintes não pergunta mais nada — roda sozinho.

```bash
go build ./cmd/coletor
./coletor
```

Cross-compile pra Windows:
```bash
GOOS=windows GOARCH=amd64 go build -o coletor.exe ./cmd/coletor
```

## Armadilha importante do `NFeDistribuicaoDFe`

Diferente de muitos webservices REST comuns, esse **pune reinício do zero**:
depois da primeira consulta bem-sucedida, é obrigatório continuar exatamente
do `ultNSU` que o próprio serviço informou. Reiniciar do zero repetidamente
derruba `cStat=656` ("Consumo Indevido") com bloqueio de **1 hora**. O
checkpoint em disco (`internal/nfedist/checkpoint.go`) existe justamente pra
nunca cometer esse erro.

## Estrutura

```
cmd/
  coletor/      — programa principal: roda as duas coletas + auto-agenda no Windows
  simulacao/    — roda com dado sintético (testdata/), sem tocar em API real

internal/
  appconfig/    — assistente de configuração na primeira execução
  certload/     — carrega certificado .pfx direto (Go puro, sem OpenSSL)
  wintask/      — auto-cadastro no Agendador de Tarefas do Windows
  nfedist/      — cliente do NFeDistribuicaoDFe (NFe/CT-e)
  adn/          — cliente do Sistema Nacional NFS-e
  document/     — parsers dos schemas XML oficiais (NFe/NFCe, NFSe, eventos)
  organizer/    — nome de arquivo + organização de pasta por data
  sieg/         — cliente de API paga (SIEG) — não usado no fluxo atual, mantido incompleto
```

## Licença

Sem licença definida ainda — uso pessoal/educacional por enquanto.
