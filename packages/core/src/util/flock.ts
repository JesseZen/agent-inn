export type FlockGlobal = {
  state: string
}

export namespace Flock {
  let global: FlockGlobal | undefined

  export function setGlobal(value: FlockGlobal) {
    global = value
  }

  export function getGlobal() {
    return global
  }

  export async function withLock<T>(_key: string, fn: () => Promise<T> | T): Promise<T> {
    return await fn()
  }
}
