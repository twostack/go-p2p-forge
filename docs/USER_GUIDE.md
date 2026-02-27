# go-p2p-forge User Guide

A comprehensive guide for developers building applications with go-p2p-forge.

---

## Table of Contents

1. [Introduction & Core Concepts](#1-introduction--core-concepts)
2. [Getting Started](#2-getting-started)
3. [The Middleware Pipeline](#3-the-middleware-pipeline)
4. [Codec & Serialization](#4-codec--serialization)
5. [Server Configuration](#5-server-configuration)
6. [Protocol Handlers](#6-protocol-handlers)
7. [Server-Initiated Communication](#7-server-initiated-communication)
8. [Dependency Injection](#8-dependency-injection)
9. [Connection Events & Presence](#9-connection-events--presence)
10. [PubSub & Topics](#10-pubsub--topics)
11. [Background Services](#11-background-services)
12. [Rate Limiting](#12-rate-limiting)
13. [Observability](#13-observability)
14. [Health Checks](#14-health-checks)
15. [Testing](#15-testing)
16. [Production Deployment](#16-production-deployment)
17. [API Quick Reference](#17-api-quick-reference)

---

## 1. Introduction & Core Concepts

go-p2p-forge is a **Netty-inspired Go framework** for building performant libp2p server applications. It extracts common patterns from production P2P services into a reusable toolkit, letting you focus on business logic instead of connection management, framing, and lifecycle orchestration.

### What forge gives you over raw libp2p

| Concern | Raw libp2p | With forge |
|---|---|---|
| Stream handling | Manual goroutine + read/write | Middleware pipeline with typed request/response |
| Wire format | Roll your own | Length-prefixed frames with buffer pooling |
| Rate limiting | Build it yourself | Per-peer sliding window, drop-in middleware |
| Panic recovery | Process crashes | Middleware catches and logs panics |
| Lifecycle | Manual startup/shutdown | Ordered startup, rollback-on-failure, graceful drain |
| Testing | Need a full libp2p host | MockStream for unit testing without a network |

### Key Abstractions

**Server** is the top-level entry point. It owns the libp2p host, DHT/GossipSub node, protocol handler registrations, background services, and configuration. Analogous to Netty's `ServerBootstrap`.

**Pipeline** is an ordered chain of middleware that processes libp2p streams. It implements the Netty `ChannelPipeline` pattern: each middleware wraps the next via `next()`, enabling bidirectional request/response processing.

**Middleware** is a function with the signature `func(sc *StreamContext, next func())`. Code before `next()` runs on the inbound path; code after runs on the outbound path. Not calling `next()` short-circuits the pipeline.

**StreamContext** carries request-scoped data through the pipeline: the raw stream, peer ID, decoded request, response object, logger, error state, and a key-value bag for inter-middleware communication. Analogous to Netty's `ChannelHandlerContext`.

### Architecture

```
                Inbound                          Outbound
                ───────►                         ◄───────

          ┌──────────────┐
          │   Recovery   │  catches panics
          └──────┬───────┘
                 │
          ┌──────▼───────┐
          │  Response    │  marshals sc.Response after handler returns
          │  Writer      │
          └──────┬───────┘
                 │
          ┌──────▼───────┐
          │  Frame       │  reads length-prefixed frame into sc.RawBytes
          │  Decode      │  (pooled buffers)
          └──────┬───────┘
                 │
          ┌──────▼───────┐
          │  Rate        │  per-peer sliding window check
          │  Limiter     │
          └──────┬───────┘
                 │
          ┌──────▼───────┐
          │  JSON        │  unmarshals sc.RawBytes → sc.Request
          │  Deserialize │
          └──────┬───────┘
                 │
          ┌──────▼───────┐
          │  Handler     │  your business logic
          │  (app code)  │  sets sc.Response
          └──────────────┘
```

Each middleware calls `next()` to proceed. The pipeline executes **recursively**: middleware 0 calls `next()`, which enters middleware 1, which calls `next()` to enter middleware 2, and so on. When the innermost handler returns, control unwinds back through each middleware's post-`next()` code.

---

## 2. Getting Started

### Installation

```bash
go get github.com/twostack/go-p2p-forge
```

### Minimal Echo Server

This example creates a server that listens for ping requests and replies with a pong:

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "time"

    forge "github.com/twostack/go-p2p-forge"
    "github.com/twostack/go-p2p-forge/codec"
    "github.com/twostack/go-p2p-forge/middleware"
)

type PingRequest struct {
    Message string `json:"message"`
}

type PingResponse struct {
    Reply string `json:"reply"`
}

func main() {
    logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
    pool := codec.NewBufferPool()
    limiter := middleware.NewSingleBucket(time.Minute, 100)
    defer limiter.Close()

    pipeline := forge.NewPipeline(logger,
        middleware.Recovery(),
        forge.JSONResponseWriter(),
        forge.FrameDecodeMiddleware(pool),
        middleware.RateLimitMiddleware(limiter),
        forge.JSONDeserialize[PingRequest](),
        func(sc *forge.StreamContext, next func()) {
            req := sc.Request.(*PingRequest)
            sc.Response = &PingResponse{Reply: "pong: " + req.Message}
        },
    )

    srv := forge.NewServer(
        forge.WithPort(9000),
        forge.WithLogger(logger),
    )
    srv.Handle("/my-app/ping/1.0.0", pipeline.StreamHandler())

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()

    if err := srv.ListenAndServe(ctx); err != nil {
        logger.Error("server error", "error", err)
    }
}
```

### Frame Wire Format

All messages on the wire use a **4-byte big-endian length prefix** followed by the payload:

```
┌────────────┬──────────────────────┐
│  4 bytes   │    N bytes           │
│  uint32 BE │    payload           │
│  (length)  │                      │
└────────────┴──────────────────────┘
```

The maximum frame size is **10 MB** (`codec.MaxFrameSize`). Empty frames (length 0) are rejected.

You can read and write frames manually with `codec.ReadFrame()` and `codec.WriteFrame()`:

```go
import "github.com/twostack/go-p2p-forge/codec"

// Write a frame
payload := []byte(`{"message":"hello"}`)
codec.WriteFrame(writer, payload)

// Read a frame
data, err := codec.ReadFrame(reader)
```

---

## 3. The Middleware Pipeline

### How Middleware Chains Work

Each middleware receives a `StreamContext` and a `next` function. Calling `next()` transfers control to the next middleware in the chain:

```go
func LoggingMiddleware(sc *forge.StreamContext, next func()) {
    // INBOUND: runs before the handler
    sc.Logger.Info("request received", "peer", sc.PeerID)

    next() // proceed to next middleware / handler

    // OUTBOUND: runs after the handler returns
    sc.Logger.Info("response sent", "error", sc.Err)
}
```

### Execution Order

For a pipeline with middleware `[A, B, C, Handler]`:

```
A.before → B.before → C.before → Handler
                                     │
C.after  ← B.after  ← A.after  ←────┘
```

This bidirectional model is what makes forge's pipeline so powerful. The Response Writer middleware, for example, sits near the top of the chain but writes the response frame **after** the handler has set `sc.Response`.

### Short-Circuiting

If a middleware does **not** call `next()`, the pipeline stops. Downstream middleware and the handler never execute. This is used for:

- **Rate limiting** — reject the request before processing
- **Authentication** — block unauthorized peers
- **Validation** — reject malformed requests early

```go
func AuthMiddleware(sc *forge.StreamContext, next func()) {
    if !isAuthorized(sc.PeerID) {
        sc.Err = errors.New("unauthorized")
        return // do NOT call next() — pipeline stops here
    }
    next()
}
```

### Error Propagation

Setting `sc.Err` causes the pipeline engine to skip remaining middleware:

```go
// In pipeline.go, the run function checks:
if sc.Err != nil || idx >= len(p.middleware) {
    return
}
```

So if any middleware sets `sc.Err`, subsequent middleware in the chain will not execute.

### Writing Custom Middleware

A middleware that measures handler execution time:

```go
func TimingMiddleware(sc *forge.StreamContext, next func()) {
    start := time.Now()
    next()
    elapsed := time.Since(start)
    sc.Logger.Info("handler completed", "duration", elapsed)
}
```

A middleware that enriches the context for downstream handlers:

```go
func TenantMiddleware(sc *forge.StreamContext, next func()) {
    tenant := resolveTenant(sc.PeerID)
    sc.Set("tenant", tenant)
    next()
}

// Later, in the handler:
func handler(sc *forge.StreamContext, next func()) {
    tenant, _ := sc.Get("tenant")
    // use tenant...
}
```

### Built-In Middleware

| Middleware | Package | Purpose |
|---|---|---|
| `Recovery()` | `middleware` | Catches panics, sets `sc.Err = ErrPanic` |
| `RateLimitMiddleware(limiter)` | `middleware` | Per-peer rate limiting with SingleBucket |
| `DualRateLimitMiddleware(limiter, isWrite)` | `middleware` | Separate read/write rate limits |
| `MetricsMiddleware(collector)` | `middleware` | Stream timing and error recording |
| `OperationRouter(field, routes)` | `middleware` | Dispatch by JSON field value |
| `Chain(mws...)` | `middleware` | Compose multiple middleware into one |
| `FrameDecodeMiddleware(pool)` | `forge` | Read length-prefixed frame into `sc.RawBytes` |
| `JSONDeserialize[T]()` | `forge` | Unmarshal `sc.RawBytes` into typed `sc.Request` |
| `JSONResponseWriter()` | `forge` | Marshal `sc.Response` and write as frame |
| `DeserializeMiddleware[T](codec)` | `forge` | Generic codec deserialization |
| `ResponseWriterMiddleware(codec)` | `forge` | Generic codec response writing |

### Pipeline Construction with `Use()`

You can build pipelines incrementally:

```go
p := forge.NewPipeline(logger)
p.Use(middleware.Recovery())
p.Use(forge.JSONResponseWriter())
p.Use(forge.FrameDecodeMiddleware(pool))
p.Use(myHandler)
```

`Use()` returns the pipeline, so you can chain:

```go
p := forge.NewPipeline(logger).
    Use(middleware.Recovery()).
    Use(forge.JSONResponseWriter()).
    Use(handler)
```

---

## 4. Codec & Serialization

### Buffer Pooling

Under high throughput, allocating a new byte slice for every frame creates GC pressure. The `BufferPool` uses three tiered `sync.Pool` instances:

| Tier | Size | Typical Use |
|---|---|---|
| Small | 4 KB | Short JSON messages |
| Medium | 64 KB | Larger payloads |
| Large | 1 MB | Bulk data transfers |

Frames larger than 1 MB are allocated normally (not pooled).

```go
pool := codec.NewBufferPool()

// Read a frame using a pooled buffer
buf, err := codec.ReadFramePooled(stream, pool)
if err != nil {
    return err
}
defer buf.Release() // IMPORTANT: always release when done

data := buf.Bytes() // use the frame contents
```

The `FrameDecodeMiddleware` handles this automatically — it reads the frame into a pooled buffer and releases it in the pipeline cleanup:

```go
forge.FrameDecodeMiddleware(pool) // stores buf in sc.PoolBuf, auto-released
```

Pool statistics are available via atomic counters:

```go
fmt.Printf("hits=%d misses=%d\n", pool.Hits.Load(), pool.Misses.Load())
```

### JSON Codec

The built-in `JSONCodec` wraps `encoding/json`:

```go
forge.JSONDeserialize[MyRequest]()   // unmarshal sc.RawBytes → sc.Request
forge.JSONResponseWriter()           // marshal sc.Response → frame
```

### Custom Codecs

Implement the `Codec` interface for alternative serialization formats:

```go
type Codec interface {
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
    ContentType() string
}
```

Example Protobuf codec:

```go
type ProtobufCodec struct{}

func (ProtobufCodec) Marshal(v any) ([]byte, error) {
    msg, ok := v.(proto.Message)
    if !ok {
        return nil, fmt.Errorf("not a proto.Message")
    }
    return proto.Marshal(msg)
}

func (ProtobufCodec) Unmarshal(data []byte, v any) error {
    msg, ok := v.(proto.Message)
    if !ok {
        return fmt.Errorf("not a proto.Message")
    }
    return proto.Unmarshal(data, msg)
}

func (ProtobufCodec) ContentType() string { return "protobuf" }
```

Use it with the generic middleware factories:

```go
pbCodec := ProtobufCodec{}

pipeline := forge.NewPipeline(logger,
    middleware.Recovery(),
    forge.ResponseWriterMiddleware(pbCodec),
    forge.FrameDecodeMiddleware(pool),
    forge.DeserializeMiddleware[*pb.MyRequest](pbCodec),
    handler,
)
```

### Multi-Frame Responses

Some protocols need to send multiple frames per response (e.g., metadata followed by N result items). Forge supports this via the `FrameIterator` interface:

```go
type FrameIterator interface {
    Next() ([]byte, error) // return io.EOF when done
}
```

When `sc.Response` implements `FrameIterator`, the `ResponseWriterMiddleware` writes each frame individually instead of marshaling a single object.

**Using SliceIterator** (the simplest approach):

```go
func handler(sc *forge.StreamContext, next func()) {
    metadata := &Metadata{Count: 3}
    item1 := &Item{ID: 1, Name: "Alice"}
    item2 := &Item{ID: 2, Name: "Bob"}
    item3 := &Item{ID: 3, Name: "Charlie"}

    sc.Response = forge.NewSliceIterator(forge.JSONCodec{},
        metadata, item1, item2, item3,
    )
}
```

The client reads frames in a loop:

```go
// Client side
metadata, _ := codec.ReadFrame(stream)
for {
    data, err := codec.ReadFrame(stream)
    if err == io.EOF {
        break
    }
    // process data...
}
```

**Custom FrameIterator** for lazy/streaming data:

```go
type DBIterator struct {
    rows *sql.Rows
    codec forge.Codec
}

func (it *DBIterator) Next() ([]byte, error) {
    if !it.rows.Next() {
        return nil, io.EOF
    }
    var row MyRow
    it.rows.Scan(&row.ID, &row.Name)
    return it.codec.Marshal(&row)
}
```

> **Partial-stream caveat**: If an error occurs mid-stream (e.g., iterator failure on frame N), the client will have received frames 0 through N-1 with no trailing sentinel. `sc.Err` is set so upstream middleware can detect the incomplete response. If you need guaranteed atomicity, pre-marshal all frames before setting `sc.Response`.

---

## 5. Server Configuration

### Config Struct

The `Config` struct holds all framework-level settings:

```go
type Config struct {
    Host                 host.Config   // network, relay, transport
    Node                 node.Config   // DHT, PubSub
    IdentityFile         string        // path to identity key file
    DataDirectory        string        // data storage directory
    RateLimitWindow      time.Duration // sliding window period
    MaxRequestsPerWindow int           // max requests per window
    ShutdownTimeout      time.Duration // graceful drain timeout
}
```

### Option Functions

Configure the server using the options pattern:

```go
srv := forge.NewServer(
    forge.WithConfig(cfg),           // full config object
    forge.WithPort(9000),            // listen port
    forge.WithLogger(logger),        // structured logger
    forge.WithIdentity(privKey),     // pre-loaded private key
    forge.WithListenAddrs(           // explicit multiaddrs
        "/ip4/0.0.0.0/tcp/9000",
        "/ip4/0.0.0.0/udp/9000/quic",
    ),
    forge.WithBootstrapPeers(        // DHT bootstrap nodes
        "/ip4/1.2.3.4/tcp/4001/p2p/QmPeer1...",
    ),
    forge.WithTransport(             // custom transport
        libp2p.Transport(tcp.NewTCPTransport),
    ),
    forge.WithPreset(forge.ProductionPreset),
    forge.WithShutdownTimeout(10 * time.Second),
    forge.WithMetrics(myCollector),
)
```

### Presets

Presets modify the config with predefined values:

```go
// Development: relaxed rate limits (1000 req/min)
forge.WithPreset(forge.DevelopmentPreset)

// Production: stricter rate limits (100 req/min)
forge.WithPreset(forge.ProductionPreset)
```

### YAML Configuration

Load from a YAML file, overlaying on top of defaults:

```go
cfg := forge.DefaultConfig()
if err := forge.LoadConfigFromFile("config.yaml", cfg); err != nil {
    log.Fatal(err)
}
srv := forge.NewServer(forge.WithConfig(cfg))
```

Example `config.yaml`:

```yaml
host:
  port: 9000
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/9000"
  bootstrap_peers:
    - "/ip4/1.2.3.4/tcp/4001/p2p/QmPeer1..."
  enable_relay: true
  enable_autonat: true
  yamux_keepalive: 60s
  yamux_write_timeout: 30s

node:
  dht_mode: 0        # 0=Server, 1=Client, 2=Auto
  enable_pubsub: true

identity_file: "./data/peer_identity.key"
data_directory: "./data"
rate_limit_window: 1m
max_requests_per_window: 100
shutdown_timeout: 5s
```

### Host Configuration

The `host.Config` controls the libp2p layer:

| Field | Default | Description |
|---|---|---|
| `Port` | 0 (random) | Listen port |
| `ListenAddresses` | [] | Explicit multiaddrs (overrides Port) |
| `ExternalAddresses` | [] | Addresses to advertise for NAT traversal |
| `YamuxKeepAlive` | 60s | Yamux keep-alive interval |
| `YamuxWriteTimeout` | 30s | Yamux write timeout |
| `EnableRelay` | true | Enable circuit relay v2 |
| `EnableRelayService` | false | Act as a relay for other peers |
| `EnableAutoRelay` | false | Use bootstrap peers as relays |
| `EnableHolePunching` | false | Enable NAT hole punching |
| `EnableAutoNAT` | true | Enable AutoNAT v2 for reachability detection |

**Custom Transports**: By default, forge uses libp2p's default transports. Passing `WithTransport()` disables defaults and uses only your provided transports:

```go
import "github.com/libp2p/go-libp2p/p2p/transport/tcp"

srv := forge.NewServer(
    forge.WithTransport(libp2p.Transport(tcp.NewTCPTransport)),
    forge.WithListenAddrs("/ip4/0.0.0.0/tcp/9000"),
)
```

### Node Configuration

| Field | Default | Description |
|---|---|---|
| `DHTMode` | Server | `DHTModeServer`, `DHTModeClient`, or `DHTModeAuto` |
| `BootstrapPeers` | [] | Multiaddrs of DHT bootstrap nodes |
| `EnablePubSub` | true | Enable GossipSub |

### Identity Management

Forge uses Ed25519 keys for peer identity. By default, the server auto-generates and persists a key:

```go
// Auto-generated: saved to DataDirectory/peer_identity.key
srv := forge.NewServer(forge.WithPort(9000))
```

You can also manage identity explicitly:

```go
import "github.com/twostack/go-p2p-forge/host"

// Load from file (supports hex, base64, or raw 32-byte formats)
priv, _ := host.LoadIdentityFromFile("./my-identity.key")
srv := forge.NewServer(forge.WithIdentity(priv))

// Load or create (auto-persists on first run)
priv, _ := host.LoadOrCreateIdentity("./data/identity.key")

// Create from deterministic seed
seed := sha256.Sum256([]byte("my-secret-seed"))
priv, _ := host.LoadIdentityFromSeed(seed[:])

// Get the peer ID from a key
peerID, _ := host.PeerIDFromIdentity(priv)
```

---

## 6. Protocol Handlers

### Registering Handlers

Register protocol handlers with `Server.Handle()`. The protocol ID is a string that peers use to negotiate the stream:

```go
srv.Handle("/my-app/ping/1.0.0", pipeline.StreamHandler())
srv.Handle("/my-app/chat/1.0.0", chatPipeline.StreamHandler())
```

A `Pipeline.StreamHandler()` returns the pipeline's `HandleStream` method as a `network.StreamHandler`, which is what libp2p expects.

### Multiple Protocols

Each protocol gets its own pipeline with potentially different middleware:

```go
// Public protocol: rate limited, no auth
publicPipeline := forge.NewPipeline(logger,
    middleware.Recovery(),
    forge.JSONResponseWriter(),
    forge.FrameDecodeMiddleware(pool),
    middleware.RateLimitMiddleware(publicLimiter),
    forge.JSONDeserialize[PublicRequest](),
    publicHandler,
)

// Admin protocol: no rate limit, auth required
adminPipeline := forge.NewPipeline(logger,
    middleware.Recovery(),
    forge.JSONResponseWriter(),
    forge.FrameDecodeMiddleware(pool),
    authMiddleware,
    forge.JSONDeserialize[AdminRequest](),
    adminHandler,
)

srv.Handle("/my-app/public/1.0.0", publicPipeline.StreamHandler())
srv.Handle("/my-app/admin/1.0.0", adminPipeline.StreamHandler())
```

### Operation Routing

When a single protocol handles multiple operation types (multiplexed on a JSON field), use `OperationRouter`:

```go
routes := map[string]forge.Middleware{
    "retrieve": middleware.Chain(
        forge.JSONDeserialize[RetrieveRequest](),
        retrieveHandler,
    ),
    "store": middleware.Chain(
        forge.JSONDeserialize[StoreRequest](),
        storeHandler,
    ),
    "delete": middleware.Chain(
        forge.JSONDeserialize[DeleteRequest](),
        deleteHandler,
    ),
}

pipeline := forge.NewPipeline(logger,
    middleware.Recovery(),
    forge.JSONResponseWriter(),
    forge.FrameDecodeMiddleware(pool),
    middleware.RateLimitMiddleware(limiter),
    middleware.OperationRouter("op", routes),
)
```

The router reads the `"op"` field from the raw JSON in `sc.RawBytes` and dispatches to the matching handler. Each route is a middleware (or chain of middleware) that handles its specific operation type.

Requests look like:

```json
{"op": "retrieve", "id": "abc123"}
{"op": "store", "id": "abc123", "data": "..."}
{"op": "delete", "id": "abc123"}
```

**Placement**: `OperationRouter` must come **after** `FrameDecodeMiddleware` (so `sc.RawBytes` is populated) and **before** any type-specific deserialization.

**Error handling**:
- Missing operation field: `sc.Err = middleware.ErrMissingOperation`
- Unknown operation value: `sc.Err = middleware.ErrUnknownOperation`

### Composing Sub-Pipelines with Chain

`middleware.Chain()` composes multiple middleware into a single middleware. This is essential for operation router routes that need deserialization + handling:

```go
middleware.Chain(
    forge.JSONDeserialize[MyRequest](),
    func(sc *forge.StreamContext, next func()) {
        req := sc.Request.(*MyRequest)
        sc.Response = processRequest(req)
    },
)
```

`Chain` checks `sc.Err` between each middleware, so if deserialization fails, the handler is skipped.

---

## 7. Server-Initiated Communication

### Outbound Streams

Most P2P communication is bidirectional — your server may need to push data to peers. Use `Server.OpenStream()`:

```go
sc, err := srv.OpenStream(ctx, targetPeerID, "/my-app/notify/1.0.0")
if err != nil {
    logger.Error("failed to open stream", "peer", targetPeerID, "error", err)
    return
}
defer sc.Stream.Close()

// Write frames directly using the codec package
notification := &Notification{Event: "new-message", Payload: "..."}
data, _ := json.Marshal(notification)
if err := codec.WriteFrame(sc.Stream, data); err != nil {
    logger.Error("failed to send notification", "error", err)
}
```

The returned `StreamContext` has `Ctx`, `Stream`, `PeerID`, and `Logger` pre-populated. The caller is responsible for writing frames and closing the stream.

### Push Notification Pattern

A common pattern is to maintain a set of connected peers and push updates:

```go
var connectedPeers sync.Map // peer.ID → struct{}

srv := forge.NewServer(
    forge.OnPeerConnected(func(pid peer.ID) {
        connectedPeers.Store(pid, struct{}{})
    }),
    forge.OnPeerDisconnected(func(pid peer.ID) {
        connectedPeers.Delete(pid)
    }),
)

// Push to all connected peers
func broadcast(ctx context.Context, srv *forge.Server, msg []byte) {
    connectedPeers.Range(func(key, _ any) bool {
        pid := key.(peer.ID)
        sc, err := srv.OpenStream(ctx, pid, "/my-app/notify/1.0.0")
        if err != nil {
            return true // continue to next peer
        }
        codec.WriteFrame(sc.Stream, msg)
        sc.Stream.Close()
        return true
    })
}
```

> **Note**: `OpenStream` requires the server to be started. Calling it before `Start()` returns `forge.ErrServerNotStarted`.

---

## 8. Dependency Injection

### The Registry

Forge provides a type-safe service locator for sharing server-level singletons (database connections, caches, config) across handlers:

```go
srv := forge.NewServer(forge.WithPort(9000))

// Register singletons before starting
srv.Provide("db", dbPool)
srv.Provide("cache", redisClient)
srv.Provide("config", appConfig)
```

### Accessing Services in Handlers

Use `forge.ServiceFrom[T]()` to retrieve typed services from the `StreamContext`:

```go
func myHandler(sc *forge.StreamContext, next func()) {
    db, ok := forge.ServiceFrom[*pgxpool.Pool](sc, "db")
    if !ok {
        sc.Err = errors.New("database not available")
        return
    }

    cache, _ := forge.ServiceFrom[*redis.Client](sc, "cache")

    // Use db and cache...
    sc.Response = &MyResponse{Data: result}
}
```

### Connecting Pipeline to Registry

Pipelines need to be connected to the server's registry so that `sc.Registry` is populated:

```go
pipeline := forge.NewPipeline(logger, /* middleware... */)
pipeline.WithRegistry(srv.Registry())

srv.Handle("/my-app/protocol/1.0.0", pipeline.StreamHandler())
```

### When to Use DI vs Closures

**Use the Registry** when:
- Multiple pipelines share the same dependency
- The dependency is a long-lived singleton (DB pool, cache client)
- You want pipelines to be testable with different registries

**Use closures** when:
- The dependency is specific to one handler
- Setup is simple and doesn't benefit from indirection

```go
// Closure approach (simpler for single-use)
myDB := connectDB()
handler := func(sc *forge.StreamContext, next func()) {
    result := myDB.Query(...)
    sc.Response = result
}
```

> **Duplicate keys**: `Provide()` panics if you register the same key twice. This is a fail-fast design to catch configuration errors at startup.

---

## 9. Connection Events & Presence

### Callbacks

Register callbacks to track when peers connect and disconnect:

```go
srv := forge.NewServer(
    forge.OnPeerConnected(func(pid peer.ID) {
        log.Printf("peer connected: %s", pid)
    }),
    forge.OnPeerDisconnected(func(pid peer.ID) {
        log.Printf("peer disconnected: %s", pid)
    }),
)
```

Multiple callbacks can be registered. They execute in registration order.

### Async Execution

Connection callbacks are dispatched **asynchronously** via goroutines. This means:

- Callbacks never block libp2p's network event loop
- Callbacks may execute concurrently with each other
- There is no guaranteed ordering between connect and disconnect events for rapid reconnections
- Panics in callbacks are caught and logged (they do not crash the server)

### Building a Presence Cache

```go
type PresenceTracker struct {
    peers sync.Map // peer.ID → time.Time (connected at)
}

func (pt *PresenceTracker) OnConnect(pid peer.ID) {
    pt.peers.Store(pid, time.Now())
}

func (pt *PresenceTracker) OnDisconnect(pid peer.ID) {
    pt.peers.Delete(pid)
}

func (pt *PresenceTracker) IsOnline(pid peer.ID) bool {
    _, ok := pt.peers.Load(pid)
    return ok
}

func (pt *PresenceTracker) Count() int {
    count := 0
    pt.peers.Range(func(_, _ any) bool { count++; return true })
    return count
}

// Usage
tracker := &PresenceTracker{}
srv := forge.NewServer(
    forge.OnPeerConnected(tracker.OnConnect),
    forge.OnPeerDisconnected(tracker.OnDisconnect),
)

// Make it available to handlers
srv.Provide("presence", tracker)
```

---

## 10. PubSub & Topics

### Joining Topics

After the server starts, access the Node to manage GossipSub topics:

```go
srv := forge.NewServer(/* ... */)
srv.Start(ctx)

node := srv.Node()

// Join a topic
if err := node.JoinTopic("my-app/events"); err != nil {
    log.Fatal(err)
}
```

`JoinTopic` is safe to call concurrently. Concurrent calls for the same topic name are serialized, but calls for different topics do not block each other.

### Publishing

```go
msg := []byte(`{"event":"user-joined","user":"alice"}`)
if err := node.Publish(ctx, "my-app/events", msg); err != nil {
    logger.Error("publish failed", "error", err)
}
```

`Publish` performs a lock-free read on the topic map — it does not contend with other operations.

### Subscribing

```go
sub := node.Subscribe("my-app/events")
if sub == nil {
    log.Fatal("topic not joined")
}

// Read messages in a goroutine
go func() {
    for {
        msg, err := sub.Next(ctx)
        if err != nil {
            return // context cancelled or subscription closed
        }
        // Skip messages from ourselves
        if msg.ReceivedFrom == node.PeerID() {
            continue
        }
        log.Printf("received from %s: %s", msg.ReceivedFrom, msg.Data)
    }
}()
```

### DHT Status

Log the current DHT routing table for debugging:

```go
node.LogDHTStatus()
// Output: DHT status routing_table_size=5 routing_table_peers=[Qm...] connected_peers=3
```

---

## 11. Background Services

### The Service Interface

Any background component can participate in the server's lifecycle by implementing:

```go
type Service interface {
    Name() string
    Start(ctx context.Context) error
    Stop() error
}
```

`Start` must not block — spawn goroutines internally. The context is cancelled on shutdown.

### Registering Services

```go
srv.AddService(myService)
srv.AddService(anotherService)
```

Services start in registration order during `Server.Start()`. If any service fails to start, previously-started services are rolled back (stopped in reverse order).

### Lifecycle Guarantees

```
Startup:   Service A → Service B → Service C  (registration order)
Shutdown:  Service C → Service B → Service A  (reverse order)
Rollback:  If B fails to start → A is stopped
```

### TickerService

The most common background task pattern is "do work on a timer." `TickerService` handles this:

```go
import "github.com/twostack/go-p2p-forge/service"

announcer := service.NewTickerService(
    "dht-announcer",         // name (for logging)
    30 * time.Second,        // interval
    func(ctx context.Context) error {
        // This runs immediately on start, then every 30 seconds
        return announcePresence(ctx)
    },
    logger,
)

srv.AddService(announcer)
```

TickerService features:
- **Immediate execution**: Work runs once on `Start()`, not waiting for the first tick
- **Panic recovery**: Panics in the work function are caught and logged
- **Health tracking**: Query health state for readiness checks

```go
announcer.Healthy()   // true if last tick succeeded
announcer.LastRun()   // time.Time of last execution
announcer.LastError() // nil or last error
```

### Custom Service Example

```go
type MetricsExporter struct {
    pool   *codec.BufferPool
    cancel context.CancelFunc
}

func (m *MetricsExporter) Name() string { return "metrics-exporter" }

func (m *MetricsExporter) Start(ctx context.Context) error {
    ctx, m.cancel = context.WithCancel(ctx)
    go func() {
        ticker := time.NewTicker(10 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                fmt.Printf("buffer pool: hits=%d misses=%d\n",
                    m.pool.Hits.Load(), m.pool.Misses.Load())
            }
        }
    }()
    return nil
}

func (m *MetricsExporter) Stop() error {
    m.cancel()
    return nil
}
```

---

## 12. Rate Limiting

### SingleBucket

Per-peer sliding window rate limiter. Each peer gets an independent bucket:

```go
// Allow 100 requests per minute per peer
limiter := middleware.NewSingleBucket(time.Minute, 100)
defer limiter.Close() // stops background cleanup goroutine
```

Use as middleware:

```go
middleware.RateLimitMiddleware(limiter)
```

When a request exceeds the limit, the middleware sets `sc.Err = forge.ErrRateLimited` and short-circuits the pipeline. No response is written.

### DualBucket

Separate limits for read and write operations:

```go
// 200 reads/min, 50 writes/min per peer
limiter := middleware.NewDualBucket(time.Minute, 200, 50)
defer limiter.Close()

middleware.DualRateLimitMiddleware(limiter, func(raw []byte) bool {
    // Classify the request by inspecting raw bytes
    return bytes.Contains(raw, []byte(`"write"`))
})
```

### Scalability

The rate limiter uses **sharded locking** (64 shards) with FNV hash distribution. This means:
- Requests from different peers rarely contend on the same lock
- A background goroutine periodically evicts entries for disconnected peers
- Call `Close()` when the limiter is no longer needed to stop the cleanup goroutine

### Per-Protocol Limiters

For different rate limits per protocol, create separate limiters:

```go
chatLimiter := middleware.NewSingleBucket(time.Minute, 1000)   // chatty
apiLimiter  := middleware.NewSingleBucket(time.Minute, 100)    // conservative
defer chatLimiter.Close()
defer apiLimiter.Close()

chatPipeline := forge.NewPipeline(logger,
    middleware.RateLimitMiddleware(chatLimiter), /* ... */)
apiPipeline := forge.NewPipeline(logger,
    middleware.RateLimitMiddleware(apiLimiter), /* ... */)
```

---

## 13. Observability

### MetricsCollector Interface

Forge defines a `MetricsCollector` interface that you implement for your preferred backend:

```go
type MetricsCollector interface {
    StreamStarted(protocol string, peerID string)
    StreamCompleted(protocol string, peerID string, durationMs float64, err error)
    RateLimitHit(peerID string)
    ActiveStreams(count int64)
    BufferPoolStats(hits, misses int64)
}
```

If no collector is configured, `NoopMetrics` is used (zero overhead).

### Example: Prometheus Collector

```go
type PrometheusCollector struct {
    streamsStarted   *prometheus.CounterVec
    streamDuration   *prometheus.HistogramVec
    rateLimitCounter *prometheus.CounterVec
    activeGauge      prometheus.Gauge
}

func (p *PrometheusCollector) StreamStarted(proto, peer string) {
    p.streamsStarted.WithLabelValues(proto).Inc()
}

func (p *PrometheusCollector) StreamCompleted(proto, peer string, durationMs float64, err error) {
    status := "ok"
    if err != nil {
        status = "error"
    }
    p.streamDuration.WithLabelValues(proto, status).Observe(durationMs)
}

func (p *PrometheusCollector) RateLimitHit(peer string) {
    p.rateLimitCounter.WithLabelValues(peer).Inc()
}

func (p *PrometheusCollector) ActiveStreams(count int64) {
    p.activeGauge.Set(float64(count))
}

func (p *PrometheusCollector) BufferPoolStats(hits, misses int64) {
    // Export as gauges
}
```

### MetricsMiddleware

Place the metrics middleware near the top of the pipeline (after Recovery, before everything else) to capture the full request lifecycle:

```go
pipeline := forge.NewPipeline(logger,
    middleware.Recovery(),
    middleware.MetricsMiddleware(myCollector),
    forge.JSONResponseWriter(),
    forge.FrameDecodeMiddleware(pool),
    // ...
)
```

### Configure on Server

```go
srv := forge.NewServer(
    forge.WithMetrics(myCollector),
)

// Access later
collector := srv.Metrics()
```

---

## 14. Health Checks

### Setup

The `HealthCheck` service exposes HTTP endpoints for orchestrator integration:

```go
import "github.com/twostack/go-p2p-forge/service"

hc := service.NewHealthCheck(":8080", logger)

// Add readiness checks
hc.AddCheck(func() error {
    if !dbPool.Ping(context.Background()) {
        return errors.New("database unreachable")
    }
    return nil
})

srv.AddService(hc)
```

### Endpoints

| Endpoint | Purpose | Behavior |
|---|---|---|
| `/healthz` | Liveness probe | Always returns `200 OK` |
| `/readyz` | Readiness probe | Returns `200 OK` if all checks pass; `503` otherwise |

### Integrating with TickerService

Connect `TickerService.Healthy()` to readiness:

```go
announcer := service.NewTickerService("announcer", 30*time.Second, work, logger)
srv.AddService(announcer)

hc := service.NewHealthCheck(":8080", logger)
hc.AddCheck(func() error {
    if !announcer.Healthy() {
        return fmt.Errorf("announcer unhealthy: %v", announcer.LastError())
    }
    return nil
})
srv.AddService(hc)
```

### Kubernetes Deployment

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
```

---

## 15. Testing

### MockStream

The `forgetest` package provides mock implementations for testing pipelines without a real libp2p network:

```go
import (
    "encoding/json"
    "testing"

    forge "github.com/twostack/go-p2p-forge"
    "github.com/twostack/go-p2p-forge/forgetest"
)

func TestPingHandler(t *testing.T) {
    // Create a mock stream with a framed JSON request
    req := PingRequest{Message: "hello"}
    reqData, _ := json.Marshal(req)
    stream := forgetest.NewMockStreamWithFrame(
        forgetest.GenerateTestPeerID(),
        reqData,
    )

    // Run the pipeline
    pipeline.HandleStream(stream)

    // Read and verify the response
    respData, err := stream.ReadResponseFrame()
    if err != nil {
        t.Fatal(err)
    }

    var resp PingResponse
    json.Unmarshal(respData, &resp)
    if resp.Reply != "pong: hello" {
        t.Errorf("unexpected reply: %s", resp.Reply)
    }
}
```

### MockStream Variants

```go
// Raw bytes (no framing) — for testing custom frame handling
stream := forgetest.NewMockStream(peerID, rawBytes)

// Pre-framed (4-byte length prefix applied) — for testing standard pipelines
stream := forgetest.NewMockStreamWithFrame(peerID, payload)

// Random peer ID
pid := forgetest.GenerateTestPeerID()
```

### Testing Middleware in Isolation

```go
func TestAuthMiddleware(t *testing.T) {
    pid := forgetest.GenerateTestPeerID()
    stream := forgetest.NewMockStream(pid, nil)

    sc := &forge.StreamContext{
        Stream: stream,
        PeerID: pid,
        Logger: slog.Default(),
    }

    called := false
    AuthMiddleware(sc, func() { called = true })

    if !called {
        t.Error("expected next to be called for authorized peer")
    }
}
```

### Testing with the Registry

```go
func TestHandlerWithDI(t *testing.T) {
    reg := forge.NewRegistry()
    reg.Provide("db", mockDB)

    pipeline := forge.NewPipeline(slog.Default(), /* middleware... */)
    pipeline.WithRegistry(reg)

    stream := forgetest.NewMockStreamWithFrame(
        forgetest.GenerateTestPeerID(),
        reqData,
    )
    pipeline.HandleStream(stream)

    // Verify...
}
```

### Race Detector

Always test with the race detector:

```bash
go test ./... -race -count=1
```

This catches concurrent access issues in your handlers and middleware.

---

## 16. Production Deployment

### Graceful Shutdown

By default, the server waits up to 5 seconds for active streams to drain during shutdown:

```go
srv := forge.NewServer(
    forge.WithShutdownTimeout(10 * time.Second),
)
```

Set to `0` for immediate shutdown (no draining).

### Active Stream Tracking

For graceful draining to work, connect pipelines to the server's active stream tracker:

```go
pipeline := forge.NewPipeline(logger, /* ... */)
pipeline.WithActiveStreams(srv.ActiveStreams())

srv.Handle("/my-app/protocol/1.0.0", pipeline.StreamHandler())
```

When `Stop()` is called:
1. Stream handlers are removed (no new streams accepted)
2. Server waits for `activeStreams.Wait()` or timeout, whichever comes first
3. Services are stopped in reverse order
4. Node and host are closed

### Signal Handling

The standard production pattern:

```go
ctx, cancel := signal.NotifyContext(context.Background(),
    os.Interrupt, syscall.SIGTERM)
defer cancel()

if err := srv.ListenAndServe(ctx); err != nil {
    logger.Error("server error", "error", err)
    os.Exit(1)
}
```

### Multiple Instances as a P2P Mesh

Each forge server instance is an **independent P2P node** with its own identity. To run multiple instances:

1. **Each instance gets a unique identity** — store identity files in instance-specific directories
2. **Use bootstrap peers** to connect instances — point all instances at shared bootstrap nodes (or at each other)
3. **No load balancer needed** — P2P mesh topology means peers discover and connect directly
4. **GossipSub for coordination** — use PubSub topics to broadcast state across instances

```
Instance A ←──────→ Instance B
    ↕                    ↕
Instance C ←──────→ Instance D
```

### Identity Across Restarts

The peer ID is derived from the Ed25519 key. To maintain the same identity across restarts:

```go
srv := forge.NewServer(
    forge.WithConfig(&forge.Config{
        IdentityFile:  "/persistent-volume/identity.key",
        DataDirectory: "/persistent-volume/data",
    }),
)
```

The first time the server starts, it generates a new key and saves it. Subsequent starts load the existing key.

### Production Checklist

- [ ] Set `WithPreset(forge.ProductionPreset)` or tune rate limits explicitly
- [ ] Configure `WithShutdownTimeout()` appropriate to your longest-running handler
- [ ] Connect `WithActiveStreams()` to all pipelines for graceful draining
- [ ] Set up health checks with readiness probes for all critical dependencies
- [ ] Use persistent storage for identity files
- [ ] Configure bootstrap peers for DHT connectivity
- [ ] Call `limiter.Close()` on shutdown to stop cleanup goroutines
- [ ] Run tests with `-race` before deploying
- [ ] Set structured logging with appropriate level (`slog.LevelInfo` for production)

---

## 17. API Quick Reference

### Package `forge` (root)

| Symbol | Kind | Description |
|---|---|---|
| `Server` | struct | Main server; owns host, node, handlers, services |
| `NewServer(opts ...Option)` | func | Create server with options |
| `WithConfig`, `WithPort`, `WithLogger`, `WithIdentity` | Option | Core configuration |
| `WithListenAddrs`, `WithBootstrapPeers`, `WithTransport` | Option | Network configuration |
| `WithPreset`, `WithMetrics`, `WithShutdownTimeout` | Option | Behavior configuration |
| `OnPeerConnected`, `OnPeerDisconnected` | Option | Connection event callbacks |
| `Server.Handle(id, handler)` | method | Register protocol handler |
| `Server.AddService(svc)` | method | Register background service |
| `Server.Provide(key, value)` | method | Register DI singleton |
| `Server.OpenStream(ctx, peer, proto)` | method | Open outbound stream |
| `Server.Start(ctx)` / `Stop()` / `ListenAndServe(ctx)` | method | Lifecycle |
| `Server.Host()` / `Node()` / `PeerID()` | method | Access internals |
| `Server.ActiveStreams()` / `Metrics()` / `Registry()` | method | Access subsystems |
| `Pipeline` | struct | Ordered middleware chain |
| `NewPipeline(logger, mw...)` | func | Create pipeline |
| `Pipeline.Use(mw...)` | method | Append middleware |
| `Pipeline.WithRegistry(r)` | method | Set DI registry |
| `Pipeline.WithActiveStreams(wg)` | method | Enable drain tracking |
| `Pipeline.StreamHandler()` | method | Get `network.StreamHandler` |
| `Middleware` | type | `func(sc *StreamContext, next func())` |
| `StreamContext` | struct | Request-scoped data carrier |
| `StreamContext.Set(key, val)` / `Get(key)` | method | Inter-middleware values |
| `Config` / `DefaultConfig()` / `LoadConfigFromFile()` | types | Configuration |
| `DevelopmentPreset` / `ProductionPreset` | Preset | Config presets |
| `Registry` / `NewRegistry()` | struct | DI container |
| `Service[T](r, key)` / `ServiceFrom[T](sc, key)` | func | Typed service lookup |
| `Codec` | interface | `Marshal` / `Unmarshal` / `ContentType` |
| `JSONCodec` | struct | JSON implementation |
| `DeserializeMiddleware[T](c)` | func | Generic deserialization middleware |
| `ResponseWriterMiddleware(c)` | func | Generic response writer middleware |
| `JSONDeserialize[T]()` | func | JSON deserialization shortcut |
| `JSONResponseWriter()` | func | JSON response writer shortcut |
| `FrameDecodeMiddleware(pool)` | func | Read frame into `sc.RawBytes` |
| `FrameIterator` | interface | Multi-frame response producer |
| `SliceIterator` / `NewSliceIterator(c, items...)` | struct | Slice-based iterator |
| `MetricsCollector` / `NoopMetrics` | interface/struct | Observability hook |
| `ErrPanic` / `ErrRateLimited` / `ErrServerNotStarted` | var | Sentinel errors |

### Package `codec`

| Symbol | Description |
|---|---|
| `ReadFrame(r)` | Read length-prefixed frame |
| `WriteFrame(w, data)` | Write length-prefixed frame |
| `ReadFramePooled(r, pool)` | Read frame with buffer pooling |
| `MaxFrameSize` | 10 MB frame limit |
| `LengthPrefixSize` | 4 bytes |
| `BufferPool` / `NewBufferPool()` | Tiered sync.Pool (4KB/64KB/1MB) |
| `PoolBuffer` | Pooled buffer; call `Release()` when done |

### Package `middleware`

| Symbol | Description |
|---|---|
| `Recovery()` | Panic recovery middleware |
| `SingleBucket` / `NewSingleBucket(window, max)` | Sharded per-peer rate limiter |
| `DualBucket` / `NewDualBucket(window, maxRead, maxWrite)` | Separate read/write limits |
| `RateLimitMiddleware(limiter)` | SingleBucket middleware |
| `DualRateLimitMiddleware(limiter, isWrite)` | DualBucket middleware |
| `MetricsMiddleware(collector)` | Stream timing/error recording |
| `OperationRouter(field, routes)` | JSON field-based dispatch |
| `Chain(mws...)` | Compose multiple middleware into one |
| `ErrUnknownOperation` / `ErrMissingOperation` | Router errors |

### Package `service`

| Symbol | Description |
|---|---|
| `Service` | Interface: `Name()`, `Start(ctx)`, `Stop()` |
| `Lifecycle` / `NewLifecycle(logger)` | Ordered startup/shutdown manager |
| `TickerService` / `NewTickerService(name, interval, work, logger)` | Periodic background work |
| `TickerService.Healthy()` / `LastRun()` / `LastError()` | Health queries |
| `HealthCheck` / `NewHealthCheck(addr, logger)` | HTTP `/healthz` and `/readyz` |
| `HealthCheck.AddCheck(fn)` | Register readiness check |

### Package `host`

| Symbol | Description |
|---|---|
| `Config` / `DefaultConfig()` | Host creation configuration |
| `RelayLimits` | Circuit relay v2 resource limits |
| `Create(cfg, priv, logger, transports...)` | Create libp2p host |
| `LoadOrCreateIdentity(path)` | Load or generate Ed25519 key |
| `LoadIdentityFromFile(path)` | Load key (hex/base64/raw) |
| `LoadIdentityFromSeed(seed)` | Key from 32-byte seed |
| `PeerIDFromIdentity(priv)` | Derive peer ID from key |

### Package `node`

| Symbol | Description |
|---|---|
| `Node` / `New(ctx, cfg, host, logger)` | DHT + GossipSub wrapper |
| `Config` / `DefaultConfig()` | Node configuration |
| `DHTModeServer` / `DHTModeClient` / `DHTModeAuto` | DHT operating modes |
| `Node.JoinTopic(name)` | Join GossipSub topic |
| `Node.Publish(ctx, topic, data)` | Publish to topic |
| `Node.Subscribe(topic)` | Get topic subscription |
| `Node.LogDHTStatus()` | Log routing table state |
| `Node.PeerID()` / `Addrs()` / `Host()` / `DHT()` / `PubSub()` | Accessors |

### Package `forgetest`

| Symbol | Description |
|---|---|
| `MockStream` / `NewMockStream(peerID, data)` | Mock `network.Stream` |
| `NewMockStreamWithFrame(peerID, payload)` | Mock stream with framed payload |
| `MockStream.ReadResponseFrame()` | Read response frame from write buffer |
| `MockConn` | Mock `network.Conn` |
| `GenerateTestPeerID()` | Random peer ID for tests |
