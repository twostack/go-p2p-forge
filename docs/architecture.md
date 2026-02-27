 ┌─────────────────────────────────────────────────────────────────────┐                                                     
 │                          forge.Server                               │
 │                                                                     │                                                     
 │  ┌───────────┐  ┌───────────┐  ┌────────────┐  ┌────────────────┐   │
 │  │  Config   │  │ Registry  │  │ Lifecycle  │  │  Callbacks     │   │
 │  │ (host +   │  │ (DI for   │  │ (ordered   │  │ OnConnected()  │   │
 │  │  node +   │  │ singletons│  │  start/    │  │ OnDisconnected │   │
 │  │  app)     │  │  via      │  │  stop)     │  │  + recover()   │   │
 │  └─────┬─────┘  │ Provide())│  └──────┬─────┘  └────────────────┘   │
 │        │        └─────┬─────┘         │                             │
 │        ▼              │               ▼                             │
 │  ┌───────────┐        │     ┌──────────────────┐                    │
 │  │ host.     │        │     │ service.Service  │ (interface)        │
 │  │ Create()  │        │     │  Name()          │                    │
 │  │           │        │     │  Start(ctx)      │                    │
 │  │ Identity  │        │     │  Stop()          │                    │
 │  │ Noise     │        │     ├──────────────────┤                    │
 │  │ Yamux     │        │     │ TickerService    │ (periodic work)    │
 │  │ Transport │        │     └──────────────────┘                    │
 │  │ Relay/NAT │        │                                             │
 │  └─────┬─────┘        │                                             │
 │        ▼              │                                             │
 │  ┌───────────┐        │                                             │
 │  │ node.Node │        │                                             │
 │  │  DHT      │        │                                             │
 │  │  GossipSub│        │                                             │
 │  └───────────┘        │                                             │
 │                       │                                             │
 │  Server.Handle(protocolID, pipeline.StreamHandler())                │
 │  Server.OpenStream(ctx, peerID, protocolID) → *StreamContext        │
 │                       │                                             │
 └───────────────────────┼─────────────────────────────────────────────┘
                         │
                         ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │                        Pipeline (per protocol)                      │
  │                                                                     │
  │  Inbound stream arrives → HandleStream(s network.Stream)            │
  │                                                                     │
  │  ┌─────────────┐    StreamContext created:                          │
  │  │ StreamContext│    .Ctx, .Stream, .PeerID, .Logger                │
  │  │             │    .Registry ← from Pipeline.WithRegistry()        │
  │  │ .RawBytes   │    .values   ← Set()/Get() for middleware comms    │
  │  │ .Request    │                                                    │
  │  │ .Response   │    ServiceFrom[T](sc, key) → typed DI lookup       │
  │  │ .Err        │                                                    │
  │  │ .PoolBuf    │                                                    │
  │  └─────────────┘                                                    │
  │                                                                     │
  │  Middleware chain (each calls next() to proceed):                   │
  │                                                                     │
  │  ┌──────────────────────────────────────────────────────────────┐   │
  │  │                                                              │   │
  │  │  ┌─────────┐  ┌────────────┐  ┌────────────┐  ┌──────────┐   │   │
  │  │  │Recovery │→ │ResponseWr. │→ │FrameDecode │→ │RateLimit │   │   │
  │  │  │         │  │(wraps call)│  │(pool buf)  │  │(per-peer)│   │   │
  │  │  └─────────┘  └────────────┘  └────────────┘  └────┬─────┘   │   │
  │  │                                                    │         │   │
  │  │        ┌───────────────────────────────────────────┘         │   │
  │  │        ▼                                                     │   │
  │  │  ┌──────────────────┐    ┌──────────────────────────────┐    │   │
  │  │  │OperationRouter   │    │ Direct handler               │    │   │
  │  │  │ (reads "op" from │ OR │ (single-operation protocol)  │    │   │
  │  │  │  RawBytes, then  │    │                              │    │   │
  │  │  │  dispatches)     │    │ Deserialize[T] → handler     │    │   │
  │  │  └────────┬─────────┘    └──────────────────────────────┘    │   │
  │  │           │                                                  │   │
  │  │           ▼                                                  │   │
  │  │  routes: map[string]Middleware                               │   │
  │  │    "retrieve" → Chain(Deserialize[RetrieveReq], handler)     │   │
  │  │    "store"    → Chain(Deserialize[StoreReq], handler)        │   │
  │  │    "delete"   → Chain(Deserialize[DeleteReq], handler)       │   │
  │  │                                                              │   │
  │  └──────────────────────────────────────────────────────────────┘   │
  │                                                                     │
  │  Response flow (after next() returns back up the chain):            │
  │                                                                     │
  │  Handler sets sc.Response:                                          │
  │    • Single object     → ResponseWriterMiddleware marshals + writes │
  │    • FrameIterator     → loop: Next() → WriteFrame() until EOF      │
  │    • nil               → no response written                        │
  │                                                                     │
  └─────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────────┐
  │                     codec (wire format)                             │
  │                                                                     │
  │  Frame = [4-byte BE uint32 length] [payload bytes]                  │
  │  MaxFrameSize = 10 MB                                               │
  │                                                                     │
  │  BufferPool (3-tier sync.Pool):                                     │
  │    Small  = 4 KB   (most JSON)                                      │
  │    Medium = 64 KB  (larger docs)                                    │
  │    Large  = 1 MB   (images/blobs)                                   │
  │    >1 MB  = direct alloc (counted as miss)                          │
  │                                                                     │
  │  ReadFramePooled() → PoolBuffer (must Release())                    │
  │  WriteFrame()      → writes length + payload                        │
  └─────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────────┐
  │                     Codec Interface                                 │
  │                                                                     │
  │  type Codec interface {                                             │
  │      Marshal(v any) ([]byte, error)                                 │
  │      Unmarshal(data []byte, v any) error                            │
  │      ContentType() string                                           │
  │  }                                                                  │
  │                                                                     │
  │  Built-in: JSONCodec{}                                              │
  │  Generic:  DeserializeMiddleware[T](codec) → sc.Request             │
  │            ResponseWriterMiddleware(codec) → writes sc.Response     │
  │  Shorthand: JSONDeserialize[T]() = DeserializeMiddleware[T](JSON)   │
  │             JSONResponseWriter()  = ResponseWriterMiddleware(JSON)  │
  └─────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────────┐
  │                     forgetest (testing)                             │
  │                                                                     │
  │  MockStream   → ReadBuf (inbound), WriteBuf (outbound capture)      │
  │  MockConn     → minimal network.Conn for RemotePeer()               │
  │  GenerateTestPeerID() → random Ed25519 peer                         │
  │  NewMockStreamWithFrame() → pre-wraps payload in length-prefix      │
  │  ReadResponseFrame() → reads response from WriteBuf                 │
  └─────────────────────────────────────────────────────────────────────┘

  Key design insight: The middleware pipeline is bidirectional — code before next() is the inbound path (frame decode, rate
  limit, deserialize), code after next() is the outbound path (response writing). ResponseWriterMiddleware exploits this by
  calling next() first, then marshaling sc.Response on the way back up. This is the Netty ChannelHandler pattern adapted to Go
   closures.

