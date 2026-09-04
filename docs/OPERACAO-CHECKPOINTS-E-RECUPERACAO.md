# Operação — checkpoints, recuperação e testes sem bloquear a SEFAZ

Este documento registra a regra operacional do coletor fiscal depois dos
testes reais feitos com a base da THESIS em 2026-09-04.

## Resumo curto

O coletor tem duas coisas diferentes:

- **XMLs das notas**: são os arquivos fiscais salvos em `NFE/` e `NFS/`.
- **Memória do coletor**: fica em `_Controle/`, principalmente
  `config.json`, `catalogo.csv`, `.checkpoint-nfedist-nsu` e
  `.checkpoint-adn-nsu`.

Em produção, **não apagar a pasta `_Controle`**.

Se apagar só os XMLs, o programa não sabe que deve voltar no tempo pela
distribuição de NF-e. Ele continua a partir do checkpoint salvo e busca
somente notas novas.

## Estrutura esperada da pasta

Exemplo usado na THESIS:

```text
Y:\FINANCEIRO\NOTAS FISCAIS\_NF AUTO\
├── _Controle\
│   ├── coletor-notas-fiscais.exe
│   ├── config.json
│   ├── catalogo.csv
│   ├── .checkpoint-nfedist-nsu
│   ├── .checkpoint-adn-nsu
│   └── painel-debug.log
├── NFE\
│   ├── NFE_COMPRAS\
│   └── NFE_SERVICO\
└── NFS\
    └── NFS_SERVICO\
```

## O que acontece se apagar tudo e clicar em buscar?

Depende do que foi apagado.

### Apagou XMLs, mas manteve `_Controle`

O coletor continua seguro:

- NF-e compra continua do último `.checkpoint-nfedist-nsu`.
- NFS-e serviço continua do último `.checkpoint-adn-nsu`.
- O `catalogo.csv` ainda sabe quais notas já existiam.

Resultado: ele baixa apenas notas novas. Os XMLs antigos apagados não voltam
automaticamente pela varredura de NSU.

Para recuperar XML antigo de NF-e compra, usar o botão:

```text
Buscar por chave → Recuperar pelo catálogo
```

Esse modo consulta por chave de acesso já registrada no `catalogo.csv`.
Ele **não zera NSU** e **não reinicia a fila da SEFAZ**.

### Apagou XMLs e apagou `_Controle`

O programa perde a memória.

Isso é perigoso para NF-e compra porque a SEFAZ exige continuidade pelo
último `ultNSU` já informado. Reiniciar do zero pode gerar:

```text
cStat=656 — Consumo Indevido
```

Esse bloqueio costuma exigir aguardar cerca de 1 hora antes de nova tentativa.

### Apagou só `catalogo.csv`

O checkpoint continua seguro, mas o painel perde o índice das notas antigas.
As próximas notas novas ainda entram normalmente.

Para recompor a lista antiga, é preciso restaurar backup/mescla do catálogo.

## Diferença entre NF-e compra e NFS-e serviço

### NF-e compra / CT-e

Usa `NFeDistribuicaoDFe`.

Regra principal: **não reiniciar NSU do zero**.

O arquivo importante é:

```text
_Controle\.checkpoint-nfedist-nsu
```

Se o XML antigo foi apagado, recuperar por chave pelo catálogo.

### NFS-e serviço

Usa o Ambiente de Dados Nacional da NFS-e (ADN).

O arquivo importante é:

```text
_Controle\.checkpoint-adn-nsu
```

Nos testes reais de 2026-09-04, a NFS-e foi validada em produção e trouxe
histórico desde 2023 para a THESIS. A ADN de serviço tolerou reconstrução
do histórico melhor que a distribuição de NF-e compra.

Mesmo assim, em produção, manter o checkpoint para buscar só novidades.

## Recuperação por catálogo

O painel tem um modo específico para testes em que XMLs foram apagados:

```text
Buscar por chave → Recuperar pelo catálogo
```

Funcionamento:

1. Lê `catalogo.csv`.
2. Filtra NF-e de compra (`NFEC`) com chave de acesso.
3. Verifica se o XML apontado no catálogo ainda existe.
4. Se não existe, consulta a SEFAZ por chave.
5. Grava o XML novamente.
6. Registra nova linha no catálogo.

Limite de segurança:

```text
20 notas por rodada
```

Se ainda faltar, rodar novamente depois.

## Operação recomendada depois que sair dos testes

1. Escolher uma pasta definitiva no servidor.
2. Rodar o instalador uma vez.
3. Conferir se `_Controle` foi criado.
4. Nunca apagar `_Controle`.
5. Não limpar checkpoint manualmente.
6. Se quiser limpar XMLs de teste, fazer backup antes.
7. Para produção, deixar a tarefa agendada buscar novas notas.

## Situação da THESIS após o teste de 2026-09-04

Arquivos analisados pelo log anexado:

- `painel-debug.log`
- `catalogo.csv`
- `.checkpoint-adn-nsu`
- `.checkpoint-nfedist-nsu`

Resultado confirmado:

```text
Catálogo antes da NFS-e: 66 entradas
Catálogo depois da NFS-e: 375 entradas
NFES recebidas: 285
NFES emitidas: 24
Cancelamentos: 24
Erros NFS-e: 0
Checkpoint ADN: 339
Checkpoint NF-e compra: 4105
```

As 66 NF-e de compra conhecidas vieram de catálogos antigos anexados durante
os testes. Elas cobrem junho/2026 a setembro/2026. Para a operação atual da
THESIS, foi decidido aceitar como base prática as compras de setembro/2026
em diante e manter o processo rodando para novas notas.

## Regra para testes futuros

Se quiser testar “pasta limpa”, limpar somente:

```text
NFE\
NFS\
```

Não limpar:

```text
_Controle\
```

Se precisar testar reinstalação completa, fazer backup de `_Controle` antes.
