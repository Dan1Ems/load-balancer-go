# Explicação Código

## Struct Backend

``` go
type Backend struct {
    URL          *url.URL
    Alive        bool
    Mutex        sync.RWMutex
    ReverseProxy *httputil.ReverseProxy
    Connections  int32 // conexões ativas (atomic)
    Requests     int64 // total de requisições enviadas (atomic)
}
```

Em um balanceador de carga, um *backend* é um servidor real que realmente processa as requisições. A `struct Backend` encapsula tudo o que o balanceador precisa saber sobre cada instância de destino: seu endereço, seu estado de saúde, o proxy que fará o encaminhamento e as métricas de utilização.

`URL` - Representa o endereço completo do servidor de destino (Ex: `http://localhost:8081`). Usamos o tipo `url.URL` da biblioteca padrão porque ele já faz o parsing, separando host, porta e caminho e é utilizado diretamente na contrução do proxy reverso.

`Alive` - Indica se o backend está saudável e pronto para receber tráfego. Precisamos protegê-lo com mutex porque ele é lido a cada nova requisição e escrito periodicamente pelo *health check*; acessos concorrentes sem sincronização causariam data race.

`Mutex` - É o mutex de leitura/escrita que proteje o campo `Alive`. O uso do `RWMutex` é intencional: várias goroutines podem verificar o estado simultanemante (usando `RLock`), enquanto a atualização do estado de saúde (escrita) exige `Lock`, evitando contenção desnecessária nas leituras.

`ReverseProxy` - É o proxy reverso que que efetivamente encaminha a requisição original ao backend. Cada backend tem sua própria instância, já configurada com uma `URL` e um `http.Transport` customizável, simplificando o caminho e permitindo ajustes por destino (timeouts, keep-alive, etc.).

`Connections` - Conta o número de requisições em andamento naquele backend no exato momento. É atualizado atomicamente (via `atomic.AddInt32`) para que o algoritmo de least connections possa ter um valor consistente em qualquer instante, sem locks pesados.

`Requests` - Total acumulado de requisições já processadas pelo backend desde o inicio do balanceador. Também usa operações atômicas (`atomic.AddInt64`) para garantir que incrementos concorrentes não se percam, servindo como métrica de longo prazo para estatísticas e algoritmos baseados em contagem.

## Struct ServerPool

``` go
type ServerPool struct {
    backends []*Backend
    mu       sync.RWMutex
}
```

`ServerPool` é o agrupamento central que mantém a lista de todos os servidores de destino (backend) disponíveis. Ele é responsavel por armazenar, proteger e fornecer acesso concorrente a esses backends para os algoritmos e seleção e para as rotinas de monitoramento. Embora no código atual a lista seja preenchida apenas uma vez na inicialização, o mutex `mu` já a prepara para futuras adições ou remoções dinâmicas em execução.

`backends []*Backend` - Fatia que contém ponteiros para cada instância de `Backend` configurada. Usamos ponteiros porque cada backends carrega estado mutável (saúde, proxy, contadores) que deve ser compartilhado e acessado por todo o pool.

`mu sync.RWMutex` - Mutex de leitura/escrita que proteje o slice `backends` contra acessos concorrentes. leituras (como obter a lista para escolha ou helth check) fazem `RLock`; enventuais escritas (adição/remoção) usam `Lock`. O `RWMutex` evita contenção desnecessária, já que a maioria das operações apenas lê a lista.

## Power of Two Choices

``` go
func (s *ServerPool) ChooseBackend() *Backend {
    s.mu.RLock()
    backends := s.backends
    s.mu.RUnlock()

    var vivos []*Backend
    for _, b := range backends {
        b.Mutex.RLock()
        alive := b.Alive
        b.Mutex.RUnlock()
        if alive {
            vivos = append(vivos, b)
        }
    }

    if len(vivos) == 0 {
        return nil
    }
    if len(vivos) == 1 {
        return vivos[0]
    }

    idx1 := rand.Intn(len(vivos))
    idx2 := rand.Intn(len(vivos))
    for idx1 == idx2 {
        idx2 = rand.Intn(len(vivos))
    }

    b1 := vivos[idx1]
    b2 := vivos[idx2]

    conn1 := atomic.LoadInt32(&b1.Connections)
    conn2 := atomic.LoadInt32(&b2.Connections)

    if conn1 <= conn2 {
        return b1
    }
    return b2
}
```

`ChooseBackend` implementa o algoritmo *Power of Two Choices* para seleção de backend. Emm vez de percorrer toda a lista ou usar *round robin* fixo, ele sorteia dois backends vivos aleatóriamente e escolhe aquele com menos conexões ativas. Essa abordagem reduz significativamente a variância no comprimento das filas e melhora a distribuição de carga, mantendo o tempo de decisão constante e baixo, mesmo com muitos backends.

``` go
    s.mu.RLock()
    backends := s.backends
    s.mu.RUnlock()
```

**Cópia local de lista** - O `RLock` no pool é breve; faz-se uma cópia do slice `backends` para trabalhar de forma segura sem segurar o lock durante o laço.

``` go
    var vivos []*Backend
    for _, b := range backends {
        b.Mutex.RLock()
        alive := b.Alive
        b.Mutex.RUnlock()
        if alive {
            vivos = append(vivos, b)
        }
    }
```

**Filtragem de vivos** - Itera sobre a cópia e, para cada backend, usa o `RLock` no seu mutex para obter o estado `Alive` de forma segura. Apenas backends marcados como vivos são adicionados ao slice `vivos`.

``` go
    if len(vivos) == 0 {
        return nil
    }
    if len(vivos) == 1 {
        return vivos[0]
    }
```

**Tratamento de casos extremos** - Se não houver nenhum vivo, retorna `nil` (para que o chamador trate o erro 503). Se houver apenas um, retorna-o imediatamente, evitando sorteios desnecessários.

``` go
    idx1 := rand.Intn(len(vivos))
    idx2 := rand.Intn(len(vivos))
    for idx1 == idx2 {
        idx2 = rand.Intn(len(vivos))
    }
```

**Sorteio de dois índices distintos** - Dois números aleatórios são gerados dentro do tamanho de `vivos`. Um laço for garante que o `idx2` seja diferente do `idx1`. evitando escolher o mesmo backend duas vezes (o que invalidaria a vantagem da esoclha dupla).

``` go
    b1 := vivos[idx1]
    b2 := vivos[idx2]

    conn1 := atomic.LoadInt32(&b1.Connections)
    conn2 := atomic.LoadInt32(&b2.Connections)

    if conn1 <= conn2 {
        return b1
    }
    return b2
```

**Comparação de conexões ativas** - Usa `atomic.LoadInt32` para ler as conexões ativas de `b1` e `b2`. Como esses contadores podem ser atualizados concorrentemente, a leitura atômica fornece um valor consistente no instante da comparação. Retorna o backend com menor números de conexões; em caso de empate, o primeiro sorteado é escolhido.

## Função `lbHandler`

``` go
func lbHandler(pool *ServerPool, w http.ResponseWriter, r *http.Request) {
    backend := pool.ChooseBackend()
    if backend == nil {
        http.Error(w, "Nenhum servidor disponível", http.StatusServiceUnavailable)
        return
    }

    atomic.AddInt32(&backend.Connections, 1)
    atomic.AddInt64(&backend.Requests, 1)

    defer atomic.AddInt32(&backend.Connections, -1)

    backend.ReverseProxy.ServeHTTP(w, r)
}
```

`lbHandler` é o coração do balanceador: ele recebe cada requisição do cliente, seleciona um backend saudável usando `ChooseBackend`, atualiza os contadores de carga e encaminha a requisição através do proxy reverso. A função garante que, independente de sucesso ou falha, o contador de conexões ativas seja decrementado ao final, mantendo a métrica precisa para as próximas decisões.

``` go
    backend := pool.ChooseBackend()
    if backend == nil {
        http.Error(w, "Nenhum servidor disponível", http.StatusServiceUnavailable)
        return
    }
```

**Seleção do backend** - Chama `pool.ChooseBackend()`. Se o retorno for `nil` (nenhum backend vivo encontrado), responde com HTTP 503 e interrompe o fluxo.

``` go
    atomic.AddInt32(&backend.Connections, 1)
    atomic.AddInt64(&backend.Requests, 1)
```

**Atualização atômica de contadores** - incrementa `Connections` (conexões ativas) e `Resquests` (total acumulado) usando `atomic.AddInt32`/`AddInt64`. Essa operações são seguras concorrentemente e rápidas, essenciais para métricas usadas na seleção de carga.

``` go
    defer atomic.AddInt32(&backend.Connections, -1)
```

**`defer` para decremento** - O `defer atomic.AddInt32(&backend.Connections, -1)` é executado quando `lbHandler` retorna, mesmo se houver um panic dentro o `ServeHTTP`. Isso evita que o contador de conexões fique permanentemente elevado após o término da requisição.

``` go
    backend.ReverseProxy.ServeHTTP(w, r)
```

**Encaminhamento** - `backend.ReverseProxy.ServeHTTP(w, r)` efetivamente envia a requisição ao servidor real e escreve a resposta de volta ao cliente. O proxy utiliza o `Transport` configurado para gerenciar timeouts e conexões TCP.

## Função `CheckHealth`

``` go
func (b *Backend) CheckHealth() {
    client := http.Client{Timeout: 2 * time.Second}
    resp, err := client.Get(b.URL.String() + "/health")
    b.Mutex.Lock()
    defer b.Mutex.Unlock()
    if err != nil || resp.StatusCode != http.StatusOK {
        b.Alive = false
        return
    }
    b.Alive = true
}
```

`CheckHealth` testa se um backend específico está saudável fazendo uma requisição HTTP GET ao endpoint `/health`. Com o timeout de 2 segundos, ele atualiza o campo `Alive` do backend de acordo com o resultado, protegendo a escrita com o mutex do backend. É executado periodicamente em goroutines separadas para não travar a verificação de outros servidores.

``` go
    client := http.Client{Timeout: 2 * time.Second}
```

**Cliente HTTP com timout** - Cria um `http.Client` com `Timeout` de 2 segundos. Isso evita que a verificação fique bloqueada indefinitivamente se o backend estiver lento ou inacessível.

``` go
    resp, err := client.Get(b.URL.String() + "/health")
```

**Requisição do endpoint de saúde** - Chama `client.Get(b.URL.String() + "/health")`. É uma convenção simples: cada backend deve expor uma rota `/health` que retorne status 200 caso esteja funcional.

``` go
    b.Mutex.Lock()
    defer b.Mutex.Unlock()
```

**Proteção com mutex** - Bloqueia `m.Mutex.Lock()` antes de verificar o erro ou status e usar `defer Unlock()`. Como outras goroutines podem estar lendo `Alive` via `RLock`, a escrita exclusiva garante atomicidade de decisão e evita data races.

``` go
    if err != nil || resp.StatusCode != http.StatusOK {
        b.Alive = false
        return
    }
    b.Alive = true
```

**Atualização condicional** - Se houver erro de rede (`err !- nil`) ou o status HTTP não for 200, marca `Alive = false`. Caso contrário, `Alive = true`. Essa lógica simples trata timouts, recusas de conexão ou respostas inadequadas como sinal de insdisponibilidade.

## Health Check

``` go
func HealthCheck(pool *ServerPool, intervalo time.Duration) {
    ticker := time.NewTicker(intervalo)
    for range ticker.C {
        pool.mu.RLock()
        backends := pool.backends
        pool.mu.RUnlock()
        for _, b := range backends {
            go b.CheckHealth()
        }
    }
}
```

`HealthCheck` é a rotina em background que mantém o estado de saúde da pool atualizado. Ela usa `time.Ticker` para disparar verificações em intervalos regulares (ex.: 10 segundos). A cada cilco, obtem a lista de backends de forma segura e lança goroutines individuais para testar cada um concorrentemente, sem bloquear a verificação dos demais.

``` go
    ticker := time.NewTicker(intervalo)
    for range ticker.C {
        ...
    }
```

**Ticker periódico** - `time.NewTicker(intervalo)` emite um sinal no canal `ticker.C` a cada `intervalo`. O laço `for range` escuta esses sinais, garantindo que o health check execute em intervalos fixos.

``` go
        pool.mu.RLock()
        backends := pool.backends
        pool.mu.RUnlock()
```

**Leitura protegida da lista** - Usa o `pool.mu.RLock()` para copiar a fatia `backends` e imediatamente libera o lock. Assim, o laço seguinte pode trabalhar com a cópia sem segurar o mutex do pool, permitindo que outras operações de leitura (como `ChooseBackend`) prossigam sem atraso.

``` go
        for _, b := range backends {
            go b.CheckHealth()
        }
```

**Verificação concorrente** - Para cada backend, dispara `go b.CheckHealth()`. Cada goroutine executa sua própria verificação e atualiza o campo `Alive` de forma independente, aproveitando o mutex já presente no `Backend`. O resultado é uma checagem rápida e não-bloqueante do conjunto inteiro.

## Função `statsHandler`

``` go
func statsHandler(pool *ServerPool, w http.ResponseWriter, r *http.Request) {
    pool.mu.RLock()
    backends := pool.backends
    pool.mu.RUnlock()

    w.Header().Set("Content-Type", "application/json")
    fmt.Fprintf(w, "{\n  \"backends\": [\n")
    for i, b := range backends {
        b.Mutex.RLock()
        alive := b.Alive
        b.Mutex.RUnlock()
        conn := atomic.LoadInt32(&b.Connections)
        reqs := atomic.LoadInt64(&b.Requests)
        fmt.Fprintf(w, "    {\"url\":\"%s\", \"alive\":%v, \"connections\":%d, \"requests\":%d}",
            b.URL.String(), alive, conn, reqs)
        if i < len(backends)-1 {
            fmt.Fprintf(w, ",")
        }
        fmt.Fprintf(w, "\n")
    }
    fmt.Fprintf(w, "  ]\n}\n")
}
```

`statsHandler` expões um endpoint (`/stats`) que retorna um JSON com as informações de cada backend: URL, estado de saúde, conexões ativas e total de requisições processadas. Ele é útil para monitoramento externo, dashboards e depuração,fornecendo uma fotografia instatânea e segura do estado da pool.

``` go
    pool.mu.RLock()
    backends := pool.backends
    pool.mu.RUnlock()
```

**Captura de lista de backends** - Protege a leitura do slice com `pool.mu.RLock()` e copia a referência da fatia. Assim, a iteração por ocorrer sem risco de alteração concorrente na lista.

``` go
    w.Header().Set("Content-Type", "application/json")
```

**Cabeçalho JSON** - Define `Content-Type` como `application/json` e começa a escrever manualmente a estrutura JSON. Embora se pudesse usar `encoding/json`, a construção manual é direta para este formato simples e evita alocações adicionais.

``` go
    for i, b := range backends {
        b.Mutex.RLock()
        alive := b.Alive
        b.Mutex.RUnlock()
        conn := atomic.LoadInt32(&b.Connections)
        reqs := atomic.LoadInt64(&b.Requests)
        ...
    }
```

**Instrução e coleta de métricas** - Para cade backend, adquire o lock de leitura (`b.Mutex.RLock()`) apenas para obter `Alive`, liberando logo em seguida. As conexões e requisições são lidas atomicamente com `LoadInt32`/`LoadInt64`. Assim, todas as leituras são consistentes sem bloquear outras operações por mais tempo que o necessário.

``` go
    for i, b := range backends {
        ...
        fmt.Fprintf(w, "    {\"url\":\"%s\", \"alive\":%v, \"connections\":%d, \"requests\":%d}",
            b.URL.String(), alive, conn, reqs)
        if i < len(backends)-1 {
            fmt.Fprintf(w, ",")
        }
        fmt.Fprintf(w, "\n")
    }
    fmt.Fprintf(w, "  ]\n}\n")
```

**Formatação da saída** - A resposta é montada linha a linha, incluindo separadores de vírgula apropriados entre objetos e fechando o JSON corretamente.

## Função `main`

``` go
func main() {
    backendURLs := []string{
        "http://localhost:8081",
        "http://localhost:8082",
        "http://localhost:8083",
    }

    pool := &ServerPool{}

    for _, urlStr := range backendURLs {
        u, err := url.Parse(urlStr)
        if err != nil {
            log.Fatalf("Erro ao parsear URL %s: %v", urlStr, err)
        }
        proxy := httputil.NewSingleHostReverseProxy(u)
        proxy.Transport = &http.Transport{
            ResponseHeaderTimeout: 5 * time.Second,
        }
        b := &Backend{
            URL:          u,
            Alive:        true,
            ReverseProxy: proxy,
            Connections:  0,
            Requests:     0,
        }
        pool.mu.Lock()
        pool.backends = append(pool.backends, b)
        pool.mu.Unlock()
    }

    go HealthCheck(pool, 10*time.Second)

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        lbHandler(pool, w, r)
    })
    http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
        statsHandler(pool, w, r)
    })

    porta := "8080"
    log.Printf("Load Balancer Power of Two Choices rodando na porta %s", porta)
    log.Printf("Backends configurados: %v", backendURLs)
    log.Fatal(http.ListenAndServe(":"+porta, nil))
}
```

`main` é o ponto de entrada do balanceador. Ela configura o pool de backends a partir de uma lista fixa de URLs, cria um proxy reverso para cada um com timeouts apropriados, inicia a rotina de health check periódica e registra os handlers HTTP para balanceamento (`/`) e estatísticas (`/stats`). Por fim, sobre o servidor na porta 8080 e permanece em execução até ser interrompido.

``` go
    backendURLs := []string{
        "http://localhost:8081",
        "http://localhost:8082",
        "http://localhost:8083",
    }
```

**Definição estática dos backends** - Um slice de strings contém as URLs dos servidores de destino. Isso simplifica a inicialização; em um cenário real poderia vir de um arquivo de configuração ou variáveis de ambiente.

``` go
    pool := &ServerPool{}
    for _, urlStr := range backendURLs {
        u, err := url.Parse(urlStr)
        if err != nil {
            log.Fatalf("Erro ao parsear URL %s: %v", urlStr, err)
        }
        ...
    }
```

**Construção do `ServerPool`** - Cria um `ServerPool` vazio e, para cada URL, faz o parsing com url.Parse. Em caso de erro, usa `log.Fatalf` para interromper a inicialização, já que uma URL inválida impede o funcionamento correto.

``` go
    for _, urlStr := range backendURLs {
        ...
        proxy := httputil.NewSingleHostReverseProxy(u)
        proxy.Transport = &http.Transport{
            ResponseHeaderTimeout: 5 * time.Second,
        }
        b := &Backend{
            URL:          u,
            Alive:        true,
            ReverseProxy: proxy,
            Connections:  0,
            Requests:     0,
        }
        pool.mu.Lock()
        pool.backends = append(pool.backends, b)
        pool.mu.Unlock()
    }
```

**Criação de cada `Backend`** - `httputil.NewSingleHostReverseProxy(u)` gera um proxy pré-configurado. o `Transport` é substituído por um `http.Transport` com `ResponseHeaderTimeout` de 5 segundos, garantindo que o proxy não espere indefinidamente pela resposta do backend. Cada `Backend` é inicializado com `Alive = true`, contadores zerados e adicionado ao pool com `Lock` exclusivo.

``` go
    go HealthCheck(pool, 10*time.Second)
```

**Health Check em background** - Uma goroutine é lançada com `go HealthCheck(pool, 10*time.Second)`, que atualiza os estado `Alive` a cada 10 segundos.

``` go
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        lbHandler(pool, w, r)
    })
    http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
        statsHandler(pool, w, r)
    })
```

**Registro de rotas** - `http.HandleFunc` mapeia `/` para `lbHandler` e `/stats` para `statsHandler`. As closures capturam o `pool` por referência, permitindo que as funções acessem o pool no escopo de `main`.

``` go
    porta := "8080"
    log.Printf("Load Balancer Power of Two Choices rodando na porta %s", porta)
    log.Printf("Backends configurados: %v", backendURLs)
    log.Fatal(http.ListenAndServe(":"+porta, nil))
```

**Inicio do servidor** - Immprime logs informativos e inicia `http.ListenAndServe` na porta 8080. Em caso de erro fatal (ex.: porta já em uso), `log.Fatal` encerra o programa.
