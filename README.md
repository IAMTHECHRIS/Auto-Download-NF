# I.O NF Automation

Captura, extração e organização automática de documentos fiscais brasileiros
(NFe de compra, CT-e, NFS-e) direto dos webservices **oficiais e gratuitos**
da SEFAZ/Receita Federal — sem depender de serviço pago de terceiros pra
esse tipo de dado.

Distribuído como um `.exe` único pra Windows: instalador gráfico na primeira
execução, depois um painel completo pra acompanhar, buscar e organizar as
notas. Escrito em Go, cross-platform no código-fonte (Linux/Windows).

## Por quê

A maioria das ferramentas de "baixar XML de nota fiscal" no mercado é paga,
mesmo quando o dado em si já é acessível de graça pelos webservices oficiais
do governo — a autenticação é feita pelo mesmo certificado digital A1/A3 que
qualquer empresa brasileira já possui. Este projeto nasceu de descobrir isso
na prática e decidir não pagar por algo que o próprio governo já oferece.

## O que cobre hoje

| Fonte | Webservice | Cobre |
|---|---|---|
| NFe de compra / CT-e | `NFeDistribuicaoDFe` (Ambiente Nacional) | Notas onde seu CNPJ é destinatário/transportador, mais consulta avulsa por chave |
| NFS-e (nota de serviço) | Sistema Nacional NFS-e (ADN) | Recebidas, emitidas e cancelamentos |

Ambos usam **mTLS com o mesmo certificado A1** — nenhum token, nenhuma API
key paga, nenhum cadastro em serviço terceiro.

**Fora de escopo (por decisão de projeto, não limitação técnica):** NFC-e
(cupom fiscal) não tem um feed de distribuição nacional único — cada estado
trata de forma diferente, e pro volume baixo de cupom não compensou construir
27 integrações estaduais.

## Instalação (usuário final)

1. Baixe `coletor-notas-fiscais.exe` na aba [Releases](../../releases) deste
   repositório.
2. Dê duplo clique. Uma janela pede CNPJ, estado, certificado `.pfx` + senha
   e a pasta onde as notas vão ficar (escolha uma pasta **local**, fora de
   qualquer sync de nuvem — é uma área de chegada, você confere antes de
   mover pra onde quer que fique sincronizado).
3. O programa **se instala sozinho**: copia a si mesmo pra dentro da pasta
   escolhida (subpasta `_Controle`, junto de config/catálogo/logs) e se
   cadastra sozinho no Agendador de Tarefas do Windows. O `.exe` baixado
   pode ser apagado depois — a cópia instalada é que continua rodando.
4. Da segunda vez em diante, abre um **painel** em vez do assistente:
   - **Entrada / Saída** — lista do que foi recebido (NFEC/NFES, em
     sub-abas) e do que foi emitido, com ordenação e filtro por coluna
     estilo Excel.
   - **Buscar por chave** — recupera uma nota específica (44 dígitos) sem
     mexer na varredura diária — útil se um XML foi apagado sem querer.
   - **Verificar cópia** — compara com uma pasta de destino (ex: Google
     Drive), mostra o que falta copiar, deixa selecionar e gerar um `.zip`.
   - **Configuração** — troca CNPJ/estado/certificado/pasta sem refazer
     tudo, cria/verifica a tarefa agendada, configura notificação por
     e-mail e tem uma opção de desinstalação completa.

Veja `dist/LEIA-ME.txt` (gerado junto do build) pra descrição completa.

## Como funciona por baixo

1. Consulta o webservice a partir do último NSU processado (nunca do zero —
   ver nota sobre rate-limit abaixo)
2. Decodifica o XML (base64 + gzip nos dois webservices)
3. Extrai fornecedor, data, número, valor
4. Salva em `ANO/MES/TIPO/FORNECEDOR_DATA_TIPO_NUMERO_VALOR.xml` e registra
   num catálogo (`_Controle/catalogo.csv`) que alimenta o painel
5. Cancelamentos são detectados e o arquivo original é renomeado no lugar
   com uma tag de status — não cria pasta separada
6. PDFs são gerados quando o XML contém dados suficientes (NF-e completa);
   se houver notificação SMTP configurada, documentos novos são enviados por
   e-mail com XML e PDF quando disponível
7. Uma tarefa agendada roda a coleta diariamente (mais um reforço 2 min
   após o PC ligar, caso o horário normal seja perdido) — com trava
   própria pra nunca coletar mais de uma vez de verdade no mesmo dia

## Armadilha importante do `NFeDistribuicaoDFe`

Diferente de muitos webservices REST comuns, esse **pune reinício do zero**:
depois da primeira consulta bem-sucedida, é obrigatório continuar exatamente
do `ultNSU` que o próprio serviço informou. Reiniciar do zero repetidamente
derruba `cStat=656` ("Consumo Indevido") com bloqueio de **1 hora**. O
checkpoint em disco (`internal/nfedist/checkpoint.go`) existe justamente pra
nunca cometer esse erro. A consulta avulsa por chave (`consChNFe`, usada em
"Buscar por chave" no painel) é independente disso — não mexe no checkpoint.

Para operação em produção, testes com pasta limpa e recuperação de XMLs
apagados, ver:

```text
docs/OPERACAO-CHECKPOINTS-E-RECUPERACAO.md
```

## Build (desenvolvimento)

```bash
go build ./cmd/coletor
```

Cross-compile pra Windows, com a janela gráfica (WebView2 + diálogos
nativos via PowerShell):
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
go build -ldflags="-H windowsgui" -o coletor.exe ./cmd/coletor
```

## Estrutura

```
cmd/
  coletor/       — programa principal: instalador, painel, coleta e auto-agendamento
  simulacao/     — roda com dado sintético (testdata/), sem tocar em API real

internal/
  appconfig/     — configuração (config.json) e caminho da pasta _Controle
  certload/      — carrega certificado .pfx direto (Go puro, sem OpenSSL)
  wintask/       — auto-cadastro no Agendador de Tarefas do Windows
  relocador/     — auto-instalação (copia o .exe pra dentro da pasta de notas)
  instalador/    — janela gráfica de configuração inicial (WebView2)
  painel/        — janela gráfica principal (catálogo, busca, verificação, config)
  catalogo/      — índice (CSV) do que já foi baixado, usado pelo painel
  verificacao/   — compara o catálogo com uma pasta de destino
  nfedist/       — cliente do NFeDistribuicaoDFe (NFe/CT-e)
  adn/           — cliente do Sistema Nacional NFS-e
  document/      — parsers dos schemas XML oficiais (NFe/NFCe, NFSe, eventos)
  organizer/     — nome de arquivo + organização de pasta por data
```

## Licença

[MIT](LICENSE) — use, copie, modifique e distribua livremente.
