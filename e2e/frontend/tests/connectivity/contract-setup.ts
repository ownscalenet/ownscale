import { buildBridge } from './preflight'
import { scratchDir } from './environment'

export default async function setup() {
  await buildBridge()
  process.env.TMPDIR = scratchDir
}
