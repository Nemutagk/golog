# golog

Librería de logging extensible con:
- Drivers múltiples (archivo y base de datos mediante un adaptador propio; fácil extender).
- Modo síncrono o asíncrono (cola FIFO con tope, sin bloqueo para quien loguea, un worker y orden conservado).
- Batching (tamaño y timeout configurables por driver).
- Formato legible en consola y archivos (pretty JSON para objetos complejos).
- Rotación diaria opcional de archivos.
- UUID v7 para cada log.
- Configuración por opciones de `golog.Init` (las variables de entorno están **deprecadas**, ver más abajo).
- Sin dependencias externas (solo la biblioteca estándar de Go).
- Personalización del bloque de información en consola (orden y campos).

## Instalación

```bash
go get github.com/Nemutagk/golog
```

## Conceptos

| Concepto    | Descripción                                                                 |
|-------------|------------------------------------------------------------------------------|
| Driver      | Implementa Insert (y opcionalmente InsertMany)                              |
| BatchDriver | Wrapper que agrupa logs por tamaño o tiempo                                  |
| Service     | Orquesta drivers; puede ser síncrono o async (cola FIFO + un worker)         |
| Async       | Encola sin bloquear el flujo principal y entrega los logs en orden           |
| Batching    | Reduce syscalls y round trips (mejor throughput)                            |

## Uso básico

```go
import "github.com/Nemutagk/golog"

// Sin drivers: solo consola. Con drivers, se agregan por opciones:
golog.Init(
  golog.WithFileDriver("./logs", true), // path, rotación diaria
  golog.WithDatabaseDriver(database.NewDatabaseDriverAdapter(miAdaptador)), // opcional, ver "Driver de base de datos"
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

## Personalizar encabezado (bloque) en consola

Ahora se puede definir qué campos y en qué orden se imprimen antes del payload usando la variable de entorno `GOLOG_BLOCKING_INFO`.

Campos disponibles:
- time
- level
- request_id
- app
- env   (variable `APP_ENV`)
- file  (formato: ruta:línea)

Ejemplo:
```
GOLOG_BLOCKING_INFO=app,level,time,file,request_id
```

Si no se define, default:
```
level,env,time,request_id,file
```

### Limitar longitud de la ruta del archivo (solo consola)
`GOLOG_FILE_LIMIT` (entero). Si es > 0 y la ruta excede ese tamaño, se trunca preservando el final:
```
GOLOG_FILE_LIMIT=32
```
Resultado ejemplo:
```
.../cmd/service/internal/handler.go:87
```

### Timezone
`LOG_TIMEZONE` (ej: America/Mexico_City, default). Se usa para la marca de tiempo en consola y archivo, y también para el nombre del archivo con rotación diaria.

**Rotación diaria y timezone:** el nombre del archivo (`YYYY-MM-DD.log`) y el momento en que cambia de día se calculan con `LOG_TIMEZONE`, no con la hora local del servidor. Si tu servidor corre en una zona distinta (p. ej. UTC), el corte de día ocurre a medianoche de `LOG_TIMEZONE`. Para usar la hora local del servidor, define `LOG_ROTATE_LOCAL_TIME=true`.

> Versiones anteriores rotaban siempre con la hora local del servidor. Si dependes de ese comportamiento, define `LOG_ROTATE_LOCAL_TIME=true`.

## Activar modo asíncrono

```go
golog.Init(
  golog.WithFileDriver("./logs", true),
  golog.WithAsync(1, 800), // workers se ignora (un solo worker para conservar el orden); 800 = capacidad inicial de la cola
)
defer golog.Close()
```

## Configuración

Todo se configura con opciones de `golog.Init`. Prioridad: **opción > variable de entorno (deprecada) > default**.

| Opción | Default | Descripción |
|--------|---------|-------------|
| `WithFileDriver(path, rotateDaily)` | - | Driver de archivo |
| `WithDatabaseDriver(driver)` | - | Driver de base de datos (ver "Driver de base de datos") |
| `WithAsync(workers, queueSize)` | desactivado | Cola async. `workers` se conserva por compatibilidad pero se usa un solo worker; `queueSize` es la capacidad inicial |
| `WithQueueMax(n)` | 10000 | Tope de logs pendientes por cola (async y por cada batch); al llegar se descartan los nuevos |
| `WithDriverTimeout(d)` | 30s | Timeout por llamada a un driver (el contexto que recibe lo cancela) |
| `WithBatching(enabled)` | true | Activa el batching |
| `WithFileBatch(size, flush)` | 32, 100ms | Tamaño e intervalo del batch del driver de archivo |
| `WithDatabaseBatch(size, flush)` | 50, 100ms | Tamaño e intervalo del batch de drivers con `CreateMany` (base de datos) |
| `WithHeaderBlocks(blocks...)` | level, env, time, request_id, file | Campos y orden del encabezado en consola y archivo (también `app`) |
| `WithFileNameLimit(n)` | 0 | Máx. caracteres de la ruta en consola (0 = sin límite) |
| `WithAppName(name)` | `--` | Valor del bloque `app` |
| `WithAppEnv(env)` | `--` | Valor del bloque `env` |
| `WithTimezone(tz)` | America/Mexico_City | Zona de la marca de tiempo y de la rotación diaria |
| `WithRotateLocalTime(bool)` | false | La rotación diaria usa la hora local del servidor |
| `WithSilent(bool)` | false | Silencia consola y drivers (ver "Silenciamiento") |

```go
golog.Init(
  golog.WithFileDriver("./logs", true),
  golog.WithAppName("mi-app"),
  golog.WithAppEnv("prod"),
  golog.WithTimezone("America/Bogota"),
  golog.WithQueueMax(50000),
)
```

Los valores inválidos (tamaños, tope o timeout `<= 0`) se ignoran y se usa el default.

### Variables de entorno (DEPRECADAS)

> **Deprecadas:** las variables de entorno siguen funcionando como respaldo, pero **se eliminarán en una versión futura**. Migra a las opciones de arriba. Si se usa alguna que ninguna opción sobrescribió, `Init` imprime un aviso por stderr con su nombre.

golog las lee con `os.Getenv` al iniciar (no al importarse), así que si cargas un `.env` hazlo antes de llamar a `Init`. Un valor vacío cuenta como no definido. Ya no usa goenvars.

| Variable | Equivale a |
|----------|------------|
| LOG_QUEUE_MAX | `WithQueueMax` |
| LOG_DRIVER_TIMEOUT_MS | `WithDriverTimeout` (en ms) |
| LOG_BATCH_ENABLED | `WithBatching` |
| LOG_BATCH_SIZE_MONGO, LOG_BATCH_FLUSH_MS_MONGO | `WithDatabaseBatch` |
| LOG_BATCH_SIZE_FILE, LOG_BATCH_FLUSH_MS_FILE | `WithFileBatch` |
| APP_NAME | `WithAppName` |
| APP_ENV | `WithAppEnv` |
| GOLOG_BLOCKING_INFO | `WithHeaderBlocks` (lista separada por comas) |
| GOLOG_FILE_LIMIT | `WithFileNameLimit` |
| LOG_TIMEZONE | `WithTimezone` |
| LOG_ROTATE_LOCAL_TIME | `WithRotateLocalTime` |
| GOLOG_SILENT | `WithSilent` |

## Silenciamiento

El logger puede silenciarse para no imprimir a consola ni escribir a drivers.

Mecanismos (de mayor a menor prioridad de aplicación):

1. **API**: `golog.SetSilent(true)` / `golog.SetSilent(false)` y `golog.IsSilent()`.
2. **Opción**: `golog.WithSilent(true)` en `Init` (o, deprecada, la variable `GOLOG_SILENT=true`).
3. **Auto-detección de tests**: cuando el binario corre bajo `go test`
   (`testing.Testing()` devuelve `true`), el logger se silencia automáticamente.

En tests siempre puedes forzar la salida temporalmente:

```go
func TestAlgo(t *testing.T) {
    golog.SetSilent(false)        // habilita salida solo para este caso
    defer golog.SetSilent(true)
    // ...
}
```

## Batching

Criterios de flush:
- Al alcanzar tamaño (SIZE): lo hace una única goroutine de fondo; quien loguea nunca espera y el orden se conserva.
- Al vencer intervalo (FLUSH_MS)
- Al cerrar (`golog.Close()`): espera a que termine el flush final.

Los logs se insertan en el mismo orden en que se emitieron.

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
- Los archivos se crean con permisos `0640` y el directorio con `0750` (sujetos al umask; los ya existentes no se modifican).
- La hora del encabezado `[time]` es la del momento en que se emitió el log, no la de la escritura.
- El `request_id` del encabezado se escribe sin saltos de línea (`\n`/`\r` se reemplazan por espacio) para que no pueda falsificar entradas.
- Rotación diaria si `rotateDaily=true` (por `LOG_TIMEZONE`, o por hora local con `LOG_ROTATE_LOCAL_TIME=true`).
- Un log por línea (más payload pretty debajo).
- Usa buffer (32 KiB) para reducir syscalls.
- Batching adicional agrupa múltiples logs antes de flush.

## Driver de base de datos (adaptador)

golog no incluye ni invoca ningún driver de base de datos. Tú entregas un objeto que implemente `models.Driver` y golog arma los documentos y administra todo el flujo (batching, async, cierre).

```go
// Obligatorio:
type models.Driver interface {
    Create(ctx context.Context, document map[string]any) error
}
// Opcional (inserción por lotes; si no lo implementas, se inserta uno a uno):
type models.DriverMany interface {
    CreateMany(ctx context.Context, documents []map[string]any) error
}

golog.Init(
  golog.WithDatabaseDriver(database.NewDatabaseDriverAdapter(miAdaptador)),
)
```

Si el adaptador no implementa `CreateMany`, los inserts van uno a uno y un fallo se registra por stderr sin reintentar el lote (evita duplicados). Si implementa `CreateMany` y este falla, el batch reintenta cada documento por separado **con el mismo `_id`**, así que el adaptador debe tolerar duplicados de `_id` (p. ej. clave única).

Cada documento incluye: `_id` (UUID v7), `created_at`, `level`, `request_id`, `payload`, `file` y `line`.

## Extender: Nuevo driver

```go
type MyDriver struct{}
func (d *MyDriver) Create(ctx context.Context, doc map[string]any) error { /* ... */ return nil }
// (Opcional)
func (d *MyDriver) CreateMany(ctx context.Context, docs []map[string]any) error { /* ... */ return nil }

// Registro:
golog.Init(golog.WithDatabaseDriver(&MyDriver{}))
```

Si implementas `CreateMany` el batchDriver lo usará.

## Shutdown

Siempre:
```go
defer golog.Close()
```
Garantiza flush de:
- Batches pendientes
- Cola async (workers)

## Cola y orden

La cola (async y batching) es FIFO: quien llama a `golog.Log` nunca espera a que el driver termine, y un único worker entrega los logs en el orden en que se emitieron. Los inserts de fondo usan un contexto que no se cancela con el de la petición.

**Tope y pérdida de logs:** es preferible perder logs a saturar la memoria o frenar la aplicación. Cada cola admite como máximo `LOG_QUEUE_MAX` logs pendientes (default 10000, incluye los que se están escribiendo). Al alcanzarlo se **descartan los logs nuevos** (se conservan los más antiguos, en orden) y se avisa por stderr con el número de logs descartados. Un valor `<= 0` usa el default.

## Métricas sugeridas (externo)

Instrumentar (no incluido):
- Contador logs emitidos / driver
- Tamaño medio batch
- Flush count por timeout / tamaño
- Latencia driver (p95 / p99)
- Tamaño de la cola async

## Rendimiento

Optimizado:
- Documentos construidos directamente como `map[string]any` (sin doble serialización)
- Batching configurable
- Buffers y pooling (sync.Pool)
- Escrituras agrupadas

Impacto típico: reducción ~0.5–1.5 ms bajo carga por amortizar I/O.

## Limitaciones

- Pérdida de logs si el proceso se aborta sin ejecutar Close (normal en batching) o si se llena la cola (`LOG_QUEUE_MAX`).
- Sin rotación por tamaño (solo diaria). Se puede añadir.
- Los payloads se copian antes de encolarse: mapas y slices se duplican (estructuras con ciclos se cortan en 32 niveles con `<max depth>`). Los **structs** se copian solo de forma superficial: un mapa o slice *dentro* de un struct sigue compartido con tu código, así que no lo modifiques de forma concurrente mientras lo logueas.
- Un panic en un driver o al serializar un payload se captura y se registra; nunca tumba la aplicación.
- El timeout por driver solo corta drivers que respetan el `context`. Uno que se bloquee sin mirarlo (p. ej. una escritura a un disco colgado) puede retener la goroutine de fondo y, con ella, `Close()`.
- Los logs emitidos después de `Close()` se insertan de forma síncrona (no se pierden, pero no pasan por la cola).

## Roadmap (ideas)

- Rotación por tamaño + compresión.
- Métricas expuestas (Prometheus).
- Filtros por nivel / módulo.
- Reintentos con backoff.

## Licencia

Licencia propia de uso gratuito con restricciones de comercialización (ver el archivo LICENSE; no es una licencia OSI/open source). Resumen, sin sustituir al texto completo:

- **Permitido:** usar, copiar y modificar la librería e integrarla en aplicaciones y servicios, incluidos los que generan ingresos (SaaS, consultoría, servicios administrados), mientras el cobro no sea por el software en sí.
- **Prohibido:** vender, licenciar o revender la librería (original o fork) como producto independiente, ofrecerla con precio en marketplaces o catálogos, re-etiquetarla como producto propio, sub-licenciarla con una licencia más permisiva o empaquetarla para explotación por terceros.
- **Forks:** permitidos si conservan la licencia y la atribución al autor.

## Requisitos

Go 1.27 o superior (se usa el paquete estándar `uuid`).

---
Contribuciones: PRs