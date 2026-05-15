# Changelog
## Unreleased
- Añadido modo silencioso (sin consola ni drivers): API `SetSilent` / `IsSilent`, variable de entorno `GOLOG_SILENT` y auto-silenciamiento bajo `go test` (`testing.Testing()`).
- Corregido `Error(...)` que producía `%!(EXTRA string=STACK TRACE:...)` cuando se le pasaba un format string con verbos (p.ej. `Error(ctx, "msg: %s", err)`). Ahora el stack trace se imprime/almacena por separado y los verbos de formato se resuelven antes de propagar los args al driver, evitando consumir argumentos extra.

## v0.2.0
- Añadido pool asíncrono (Service) con cola bloqueante (no se pierden logs).
- Incorporado soporte de batching para todos los drivers mediante batchDriver (umbral por tamaño y timeout).
- Parámetros de batching y activación via variables de entorno: LOG_BATCH_ENABLED, LOG_BATCH_SIZE_MONGO, LOG_BATCH_FLUSH_MS_MONGO, LOG_BATCH_SIZE_FILE, LOG_BATCH_FLUSH_MS_FILE.
- Definida interfaz BulkInserter e implementación InsertMany fallback en mongoDriver.
- Eliminado doble paso json→bson (se construye bson.M directo).
- FileDriver mejorado: escritura buffered (bufio), formato sin corchetes y pretty JSON para objetos complejos.
- Consola ahora usa formateo humano con pretty JSON y pooling de buffers (sync.Pool).
- Rotación diaria opcional en FileDriver y nombres de archivo consistentes.
- Integración de batching en Init según tipo de driver.
- Close drena workers y flush pendiente (batch + async).
- Simplificación y normalización de detección de tipos simples vs complejos.
- Reducción de latencia (~1ms en pruebas) por batching y menor overhead de serialización.