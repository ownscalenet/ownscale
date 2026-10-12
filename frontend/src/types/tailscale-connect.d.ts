// Type declarations for @tailscale/connect, the WebAssembly Tailscale
// client used by the browser SSH page. The package ships no .d.ts despite
// its "types" field, so these mirror cmd/tsconnect's wasm_js.d.ts, but trimmed.
declare module '@tailscale/connect' {
  export type IPNState =
    | 'NoState'
    | 'InUseOtherUser'
    | 'NeedsLogin'
    | 'NeedsMachineAuth'
    | 'Stopped'
    | 'Starting'
    | 'Running'

  export interface IPNSSHSession {
    resize(rows: number, cols: number): boolean
    close(): boolean
  }

  export interface IPNStateStorage {
    setState(id: string, value: string): void
    getState(id: string): string
  }

  export interface IPN {
    run(callbacks: {
      notifyState: (state: IPNState) => void
      notifyNetMap: (netMapStr: string) => void
      notifyBrowseToURL: (url: string) => void
      notifyPanicRecover: (err: string) => void
    }): void
    login(): void
    logout(): void
    ssh(
      host: string,
      username: string,
      termConfig: {
        writeFn: (data: string) => void
        writeErrorFn: (err: string) => void
        setReadFn: (readFn: (data: string) => void) => void
        rows: number
        cols: number
        timeoutSeconds?: number
        onConnectionProgress: (message: string) => void
        onConnected: () => void
        onDone: () => void
      },
    ): IPNSSHSession
  }

  export function createIPN(config: {
    authKey: string
    controlURL?: string
    hostname?: string
    stateStorage?: IPNStateStorage
    wasmURL?: string
    panicHandler: (err: string) => void
  }): Promise<IPN>

  export function runSSHSession(
    termContainerNode: HTMLDivElement,
    def: { username: string; hostname: string; timeoutSeconds?: number },
    ipn: IPN,
    callbacks: {
      onConnectionProgress: (message: string) => void
      onConnected: () => void
      onDone: () => void
      onError?: (err: string) => void
    },
    terminalOptions?: { theme?: Record<string, string>; fontFamily?: string; fontSize?: number },
  ): void
}
