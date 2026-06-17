# Load Balancer

Explicação do código: [EXPLICACAO.md](EXPLICACAO.md)

## Estrutura

``` bash
.
├── backend
│   └── backend.go
├── docker-compose.yml
├── Dockerfile
├── EXPLICACAO.md
├── main.go
└── README.md
```

## Introdução

Com o crescimento constante das demandas dos serviços online, a sobrecarga de servidores tornou-se um problema comum. A necessidade de uma solução que distribua o tráfego de forma eficaz é essencial para garantir a estabilidade e performance das aplicações.

Para mitigar esses desafios, diversas técnicas foram desenvolvidas para otimizar a distribuição de cargas e garantir que os recursos sejam utilizados de maneira mais eficiente. Um dessas estratégias é amplamente utilizada e oferece diversas vantagens para a gestão de tráfego em redes complexas.

Essa técinica, conhecida como balanceamento de carga, é fundamental para otimizar o uso dos recursos e garantir a continuidade dos serviçoes, mesmo sob alta demanda.

Esse projeto implementa o algoritmo **Power of Two Choices**, que seleciona dois servidores aleatórios e encaminha a requisição para o que possui o menor número de conexões ativas. Dessa forma, o balanceador se adapta a nós com diferentes capacidades de processamento, evitando sobrecarregar servidores mais lentos.

## Funcionamento

O balanceamento de cargas é uma técnica que distribui o tráfego de rede ou as solicitações de serviço entre vários servidores. Ele garante que nenhum servidor fique sobrecarregado, permitindo uma operação mais eficiente e estável.

A distribuição ocorre de forma que cada servidor receba uma quantidade adequada de tráfego. Isso é feito através de algoritmos que avaliam a capacidade e o estado atual de cada servidor, assegurando que o tráfego seja gerenciado de maneira eficaz.

## Instruções

### **Pacotes necessários**

``` bash
    git go docker docker-compose hey jq
```

**Linux**:

``` bash
sudo pacman -S go \
    docker \
    docker-compose \
    jq \

# Instalando hey via go install
go install github.com/rakyll/hey@latest
```

**Windows**:

``` txt
Instala manualmente aí po, windows noob.
```

### **Arch Linux**

Comece clonando o repositório para que você tenha os recursos necessário para executar a aplicação.

``` bash
# Clonagem do repositório
git clone https://github.com/Dan1Ems/load-balancer-go.git

# Navegue para dentro do repositório
cd load-balancer-go
```

Para testar o Load Balancer é necessário ativar os backends do ambiente docker.

``` bash
sudo docker-compose up --build -d
```

Após executar esse comando, cada servidor será ativado em seus respectivos containers. Depois de subir os backends, temos que rodar o código principal, responsável por executar o Load Balancer (utilize em um terminal separado).

``` go
go run main.go
```

Certo, agora o Load Balancer está executando, mas não tem como saber se ele realmente está funcioando, para isso, precisaremos exergar o que está acontecendo e enviar as requisições para conseguir fazer o teste.

Agora, para fazer as requisições, é necessário utilizar o seguinte comando, que faz 2000 requisições para cada conexão aberta, que, no caso do comando abaixo, são 100:

``` go
hey -n 2000 -c 100 http://localhost:8080/
```

Para uma noção visual, há um comando que permite a visualização das estatísticas (certifique-se de ter o `jq` instalado: `sudo pacman -S jq`)

``` bash
curl http://localhost:8080/stats | jq
```

### **Windows**

Ativar backends do docker:

``` Powershell
docker-compose up --build -d
```

Executar Load Balancer (utilize em um terminal separado):

``` go
go run main.go
```

Fazer as requisições:

``` go
hey -n 2000 -c 100 http://localhost:8080/
```

Exibir estatísticas:

``` Powershell
curl http://localhost:8080/stats | jq
```

## Comportamento

Ao realizar o teste do Load Balancer no cenário do Power of Two Choices, a execução seguiu conforme o esperado. Os servidores mais lentros (latência maior) processaram uma quantidade menor de requisições, enquanto os servidores mais rápidos (latência menor) processaram uma quantidade maior de requisições.

``` json
{
  "backends": [
    {
      "url": "http://localhost:8081",
      "alive": true,
      "connections": 0,
      "requests": 1279
    },
    {
      "url": "http://localhost:8082",
      "alive": true,
      "connections": 0,
      "requests": 495
    },
    {
      "url": "http://localhost:8083",
      "alive": true,
      "connections": 0,
      "requests": 226
    }
  ]
}
```

Isso é um fenômeno natural do Power of Two Choices. Ao escolher dois servidores aleatórios, ele envia a requisição para o com a menor quantidade de requisições naquele momento. Naturalmente, o servidor mais rápido frequentemente terá uma quantidade de conexões menor que o servidor mais lento.
