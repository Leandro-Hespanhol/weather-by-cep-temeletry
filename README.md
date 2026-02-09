# Weather by CEP - OpenTelemetry + Zipkin

Sistema distribuído em Go que recebe um CEP, identifica a cidade e retorna o clima atual (temperatura em Celsius, Fahrenheit e Kelvin) com tracing distribuído usando OpenTelemetry e Zipkin.

## Arquitetura

```
┌─────────────┐     ┌─────────────┐     ┌──────────────────┐     ┌─────────┐
│   Client    │────▶│  Service A  │────▶│    Service B     │────▶│ viaCEP  │
│             │     │  (Input)    │     │ (Orchestration)  │     │   API   │
└─────────────┘     └─────────────┘     └──────────────────┘     └─────────┘
                           │                    │                      
                           │                    │               ┌─────────────┐
                           │                    └──────────────▶│ WeatherAPI  │
                           │                                    └─────────────┘
                           ▼                    ▼
                    ┌─────────────────────────────────┐
                    │      OTEL Collector             │
                    └─────────────────────────────────┘
                                    │
                                    ▼
                            ┌─────────────┐
                            │   Zipkin    │
                            │  (Tracing)  │
                            └─────────────┘
```

### Serviço A (Input Service)
- Porta: 8080
- Recebe requisições POST com CEP
- Valida formato do CEP (8 dígitos)
- Encaminha para o Serviço B

### Serviço B (Orchestration Service)
- Porta: 8081
- Consulta o CEP na API viaCEP
- Obtém a temperatura na API WeatherAPI
- Retorna cidade e temperaturas em Celsius, Fahrenheit e Kelvin

## Pré-requisitos

- Docker
- Docker Compose
- Chave de API do [WeatherAPI](https://www.weatherapi.com/) (gratuita)

## Configuração

1. Clone o repositório:
```bash
git clone <repository-url>
cd weather-open-telemetry
```

2. Crie o arquivo `.env` com sua chave da WeatherAPI:
```bash
cp .env.example .env
# Edite o .env e adicione sua chave da WeatherAPI
```

3. Obtenha sua chave gratuita em: https://www.weatherapi.com/signup.aspx

## Como Executar

### Usando Docker Compose

```bash
# Inicia todos os serviços
docker-compose up --build

# Ou em background
docker-compose up --build -d
```

Os serviços estarão disponíveis em:
- **Service A**: http://localhost:8080
- **Service B**: http://localhost:8081
- **Zipkin UI**: http://localhost:9411

### Testando a API

#### Requisição válida:
```bash
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "01310100"}'
```

Resposta esperada (HTTP 200):
```json
{
  "city": "São Paulo",
  "temp_C": 25.0,
  "temp_F": 77.0,
  "temp_K": 298.0
}
```

#### CEP inválido (formato incorreto):
```bash
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "123"}'
```

Resposta (HTTP 422):
```json
{
  "message": "invalid zipcode"
}
```

#### CEP não encontrado:
```bash
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "00000000"}'
```

Resposta (HTTP 404):
```json
{
  "message": "can not find zipcode"
}
```

## Visualizando os Traces

1. Acesse o Zipkin UI em: http://localhost:9411
2. Clique em "Run Query" para ver os traces
3. Selecione um trace para ver os spans detalhados

### Spans implementados:
- `handle-cep-request` - Processamento da requisição no Service A
- `validate-cep` - Validação do formato do CEP
- `forward-to-service-b` - Chamada HTTP para o Service B
- `handle-weather-request` - Processamento no Service B
- `lookup-cep-viacep` - Consulta à API viaCEP
- `get-weather-api` - Consulta à API WeatherAPI

## Fórmulas de Conversão

- **Fahrenheit**: F = C × 1.8 + 32
- **Kelvin**: K = C + 273

## Estrutura do Projeto

```
weather-open-telemetry/
├── service-a/
│   ├── main.go           # Serviço de entrada
│   ├── Dockerfile
│   ├── go.mod
│   └── go.sum
├── service-b/
│   ├── main.go           # Serviço de orquestração
│   ├── Dockerfile
│   ├── go.mod
│   └── go.sum
├── docker-compose.yaml
├── otel-collector-config.yaml
├── .env.example
└── README.md
```

## Parando os Serviços

```bash
docker-compose down
```

## Troubleshooting

### Erro de conexão com WeatherAPI
Verifique se a variável `WEATHER_API_KEY` está configurada corretamente no arquivo `.env`.

### Traces não aparecem no Zipkin
Aguarde alguns segundos após fazer as requisições. O OTEL Collector usa batch processing e pode haver um pequeno delay.

### Serviço B não está acessível
Verifique se todos os containers estão rodando:
```bash
docker-compose ps
```
