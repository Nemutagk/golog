# golog

Librería de logging extensible con:
- Drivers múltiples (MongoDB, archivos; fácil extender).
- Modo síncrono o asíncrono (pool de workers, cola bloqueante sin pérdida).
- Batching (tamaño y timeout configurables por driver).
- Formato legible en consola y archivos (pretty JSON para objetos complejos).
- Rotación diaria opcional de archivos.
- UUID v7 para cada log.
- Configuración vía variables de entorno.

## Instalación

```bash
go get github.com/Nemutagk/golog
```

## Conceptos

| Concepto    | Descripción                                                                 |
|-------------|------------------------------------------------------------------------------|
| Driver      | Implementa Insert (y opcionalmente InsertMany)                              |
| BatchDriver | Wrapper que agrupa logs por tamaño o tiempo                                  |
| Service     | Orquesta drivers; puede ser síncrono o async (workers + cola)                |
| Async       | Evita bloquear el flujo principal hasta que se llenen workers / backpressure |
| Batching    | Reduce syscalls y round trips (mejor throughput)                            |

## Uso básico

```go
import "github.com/Nemutagk/golog"

// Inicializar conexiones DB (usando godb) y luego:
golog.Init(conns,
  golog.WithMongoDriver(),
  golog.WithFileDriver("./logs", true), // path, rotación diaria
)
defer golog.Close()

ctx := context.Background()
golog.Log(ctx, "Iniciando", map[string]any{"version":"0.2.0"})
golog.Error(ctx, "Fallo al procesar", err)
golog.Debug(ctx, "Payload:", struct{ID int}{ID: 42})
```

Salida consola / archivo (ejemplo):
```
[2025-08-14 10:00:00 CST][INFO][/path/main.go:25]
Iniciando {"version":"0.2.0"}
```

## Activar modo asíncrono

```go
golog.Init(conns,
  golog.WithMongoDriver(),
  golog.WithFileDriver("./logs", true),
  golog.WithAsync(8, 800), // 8 workers, cola buffer 800 (envía bloqueando si llena)
)
defer golog.Close()
```

## Variables de entorno

| Variable | Default | Descripción |
|----------|---------|-------------|
| LOG_BATCH_ENABLED | true | Activa batching global |
| LOG_BATCH_SIZE_MONGO | 50 | Tamaño batch Mongo |
| LOG_BATCH_FLUSH_MS_MONGO | 100 | Flush timeout Mongo (ms) |
| LOG_BATCH_SIZE_FILE | 32 | Tamaño batch File |
| LOG_BATCH_FLUSH_MS_FILE | 100 | Flush timeout File (ms) |
| DB_LOGS_CONNECTION | logs | Nombre conexión en godb |
| APP_NAME | logs | Nombre de colección Mongo |
| (async se configura por código) | - | workers y queueSize |

Lectura con `goenvars.GetEnv*`.

## Batching

Criterios de flush:
- Al alcanzar tamaño (SIZE)
- Al vencer intervalo (FLUSH_MS)
- Al cerrar (`golog.Close()`)

Trade-offs:
- Añade latencia micro (<= flush_ms)
- Mejora throughput y reduce p95 si había I/O frecuente

Ajuste:
- Aumenta SIZE si hay mucho volumen (100–200)
- Reduce FLUSH_MS si necesitas logs casi en tiempo real

## Formateo

- Primitivos en línea.
- Objetos / mapas / slices: JSON indentado.
- Múltiples argumentos: primitivos separados por espacio; cada objeto complejo en bloque.

## FileDriver

- Escribe formato humano.
- Rotación diaria si `rotateDaily=true`.
- Un log por línea (más payload pretty debajo).
- Usa buffer (32 KiB) para reducir syscalls.
- Batching adicional agrupa múltiples logs antes de flush.

## Extender: Nuevo driver

```go
type MyDriver struct{}
func (d *MyDriver) Insert(ctx context.Context, doc bson.M) error { /* ... */ return nil }
// (Opcional)
func (d *MyDriver) InsertMany(ctx context.Context, docs []bson.M) error { /* ... */ return nil }

// Registro:
golog.Init(conns, func(c *config){ c.drivers = append(c.drivers, &MyDriver{}) })
```

Si implementas InsertMany el batchDriver lo usará.

## Shutdown

Siempre:
```go
defer golog.Close()
```
Garantiza flush de:
- Batches pendientes
- Cola async (workers)

## Backpressure

Cola bloqueante: si workers saturan, la goroutine productora espera en `s.jobs <-`. No se pierden logs. Ajusta queueSize para suavizar picos.

## Métricas sugeridas (externo)

Instrumentar (no incluido):
- Contador logs emitidos / driver
- Tamaño medio batch
- Flush count por timeout / tamaño
- Latencia driver (p95 / p99)
- Backlog cola async (len(channel))

## Rendimiento

Optimizado:
- Sin doble marshal json→bson
- Batching configurable
- Buffers y pooling (sync.Pool)
- Escrituras agrupadas

Impacto típico: reducción ~0.5–1.5 ms bajo carga por amortizar I/O.

## Limitaciones

- Pérdida de logs si el proceso se aborta sin ejecutar Close (normal en batching).
- Sin rotación por tamaño (solo diaria). Se puede añadir.

## Roadmap (ideas)

- Rotación por tamaño + compresión.
- Métricas expuestas (Prometheus).
- Filtros por nivel / módulo.
- Reintentos con backoff.

## Licencia

MIT (agregar LICENSE).

---
Contribuciones: PRs