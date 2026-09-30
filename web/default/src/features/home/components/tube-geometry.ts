/**
 * 环管几何：一根绕成环的管子，截面是椭圆，并且沿环一周截面自身扭转 180°。
 * 也就是「有厚度的莫比乌斯环」—— 截面压扁成一条线段时就退化为原来的莫比乌斯带，
 * a = b 时退化为普通的正圆环管（这时扭转对形状没影响，只体现在粒子轨迹是螺旋线上）。
 *
 * 参数方程（u 沿环周，φ 绕截面，h = u/2）：
 *   e_a = cos(h)·radial + sin(h)·ẑ      截面宽方向（原带宽方向）
 *   e_b = −sin(h)·radial + cos(h)·ẑ     截面厚方向
 *   S(u, φ) = R·radial + a·cos(φ)·e_a + b·sin(φ)·e_b
 *
 * 展开后 ρ = R + a·cosφ·cos h − b·sinφ·sin h，z = a·cosφ·sin h + b·sinφ·cos h，
 * 于是 dρ/du = −z/2，dz/du = (ρ − R)/2，沿 u 的弧长速率 |∂S/∂u| = √(ρ² + (z² + (ρ−R)²)/4)。
 * 它随 u 和 φ 都变，所以布点和推进都必须按弧长（见 invertArc）。
 *
 * 和莫比乌斯带一样：S(u + 2π, φ) = S(u, φ + π)。整根管子由 u ∈ [0, 2π) 铺满，
 * 但一颗粒子要走到 4π 才回原位。所以只取 φ ∈ [0, π) 的半数轨道、每条跑满 [0, 4π)，
 * 就能铺满整根管子且首尾天然连续。
 *
 * 不依赖 three.js 和 DOM，方便脱离渲染单独验证（间距均匀度脚本直接 import 这里）。
 */

export const TUBE = {
  ringRadius: 5.2,
  a: 1.1, // 截面宽半轴
  b: 1.1, // 截面厚半轴（a = b 即正圆管）
  gap: 0.24, // 粒子间距，环周方向与截面方向共用
}

/**
 * 两根环管互相穿过（霍普夫链环）：两环平面都含 x 轴、相互垂直（各自绕 x 轴 ±tilt），
 * 圆心沿 x 轴错开一个环半径 —— 每个环都正好穿过另一个环的圆心。
 * 两条中轴线的最近距离恒等于 R，管面之间留有 R − 2r 的空隙，不会相交。
 */
export const LINK = {
  tilt: Math.PI / 4,
  offset: TUBE.ringRadius / 2, // 圆心离装配中心的距离
  flow: [1, -1], // 两环流向相反
}

/** 第 ring 个环（0 / 1）管面上 (u, φ) 处的点，已放到链环装配坐标里 */
export function linkPoint(
  ring: number,
  u: number,
  phi: number,
  out: Float32Array | number[],
  o: number
) {
  surfacePoint(u, phi, out, o)
  const sign = ring === 0 ? 1 : -1
  const c = Math.cos(LINK.tilt)
  const s = Math.sin(LINK.tilt) * sign
  const y = out[o + 1]
  const z = out[o + 2]
  out[o] -= sign * LINK.offset
  out[o + 1] = y * c - z * s
  out[o + 2] = y * s + z * c
}

/**
 * 同 linkPoint，但同时给出该点的单位法线（渲染时按法线给粒子上色，让整根管子有明暗）。
 * 法线取椭圆截面的精确法线 ∝ (cosφ/a)·e_a + (sinφ/b)·e_b，忽略扭转对 ∂S/∂u 的微小贡献；
 * 位置与 linkPoint 逐位一致，三角函数只算一遍。
 */
export function linkFrame(
  ring: number,
  u: number,
  phi: number,
  pos: Float32Array,
  po: number,
  nrm: Float32Array,
  no: number
) {
  const h = u * 0.5
  const cu = Math.cos(u)
  const su = Math.sin(u)
  const ch = Math.cos(h)
  const sh = Math.sin(h)
  const cp = Math.cos(phi)
  const sp = Math.sin(phi)

  const ca = TUBE.a * cp
  const sb = TUBE.b * sp
  const rho = TUBE.ringRadius + ca * ch - sb * sh
  const py = rho * su
  const pz = ca * sh + sb * ch

  const na = cp / TUBE.a
  const nb = sp / TUBE.b
  const rc = na * ch - nb * sh
  const zc = na * sh + nb * ch
  const inv = 1 / Math.hypot(rc, zc)
  const ny = rc * inv * su
  const nz = zc * inv

  const sign = ring === 0 ? 1 : -1
  const c = Math.cos(LINK.tilt)
  const s = Math.sin(LINK.tilt) * sign
  pos[po] = rho * cu - sign * LINK.offset
  pos[po + 1] = py * c - pz * s
  pos[po + 2] = py * s + pz * c
  nrm[no] = rc * inv * cu
  nrm[no + 1] = ny * c - nz * s
  nrm[no + 2] = ny * s + nz * c
}

export const TWO_LOOPS = Math.PI * 4

const ARC_SAMPLES = 2048 // 弧长积分精度
const LUT_SIZE = 1024 // 等弧长反查表精度

export interface Row {
  phi: number
  lut: Float32Array
  total: number // 这条轨道跑满 [0, 4π) 的总弧长
  n: number // 粒子数
}

/** 管面上 (u, φ) 处的点，写进 out[o..o+2] */
export function surfacePoint(
  u: number,
  phi: number,
  out: Float32Array | number[],
  o: number
) {
  const h = u * 0.5
  const ca = TUBE.a * Math.cos(phi)
  const sb = TUBE.b * Math.sin(phi)
  const rho = TUBE.ringRadius + ca * Math.cos(h) - sb * Math.sin(h)
  out[o] = rho * Math.cos(u)
  out[o + 1] = rho * Math.sin(u)
  out[o + 2] = ca * Math.sin(h) + sb * Math.cos(h)
}

/** 沿 u 方向每单位角度对应的弧长 */
export function arcSpeed(u: number, phi: number) {
  const h = u * 0.5
  const ca = TUBE.a * Math.cos(phi)
  const sb = TUBE.b * Math.sin(phi)
  const dr = ca * Math.cos(h) - sb * Math.sin(h) // ρ − R
  const z = ca * Math.sin(h) + sb * Math.cos(h)
  const rho = TUBE.ringRadius + dr
  return Math.sqrt(rho * rho + (z * z + dr * dr) * 0.25)
}

/**
 * 对速率函数积分出弧长表，再反查成「归一化弧长 [0,1] → 参数」的等弧长表。
 * 返回表本身和总弧长。
 */
function invertArc(speed: (t: number) => number, span: number) {
  const dt = span / ARC_SAMPLES
  const lAt = new Float64Array(ARC_SAMPLES + 1)
  let prev = speed(0)
  let total = 0
  for (let k = 1; k <= ARC_SAMPLES; k++) {
    const cur = speed(k * dt)
    total += (prev + cur) * 0.5 * dt // 梯形积分
    lAt[k] = total
    prev = cur
  }

  const lut = new Float32Array(LUT_SIZE + 1)
  let cursor = 0
  for (let m = 0; m <= LUT_SIZE; m++) {
    const target = (m / LUT_SIZE) * total
    while (cursor < ARC_SAMPLES && lAt[cursor + 1] < target) cursor++
    const seg = lAt[cursor + 1] - lAt[cursor] || 1
    lut[m] = (cursor + (target - lAt[cursor]) / seg) * dt
  }
  return { lut, total }
}

/** 等弧长反查：归一化弧长 s ∈ [0,1) → 参数 */
export function lookup(lut: Float32Array, s: number) {
  const f = s * LUT_SIZE
  const m = f | 0
  return lut[m] + (lut[m + 1] - lut[m]) * (f - m)
}

/**
 * 布轨道：截面椭圆按弧长等分成 M 段（M 取偶数），只取前一半 φ ∈ [0, π)，
 * 另一半由同一批粒子跑第二圈时自动画出。每条轨道再按环周弧长决定粒子数。
 */
export function buildRows(): Row[] {
  const { a, b, gap } = TUBE
  const ring = invertArc(
    (phi) => Math.hypot(a * Math.sin(phi), b * Math.cos(phi)),
    Math.PI * 2
  )
  const segments = Math.max(2, 2 * Math.round(ring.total / (2 * gap)))
  const rows: Row[] = []
  for (let j = 0; j < segments / 2; j++) {
    const phi = lookup(ring.lut, (j + 0.5) / segments)
    const { lut, total } = invertArc((u) => arcSpeed(u, phi), TWO_LOOPS)
    rows.push({ phi, lut, total, n: Math.max(8, Math.round(total / gap)) })
  }
  return rows
}
