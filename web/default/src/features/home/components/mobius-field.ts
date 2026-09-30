/**
 * 鼠标力场：一颗不规则的颗粒球体。
 *
 * 球心放在视线碰到的那层环带表面上，只推这一层 —— 视线后方那层环带离球心很远，不受影响。
 * 球的半径随方向起伏、并随时间缓慢变形，所以推出来的空洞是一团不规则的形状，不是正圆；
 * 起伏里叠了几道高频，洞的边缘是一圈参差的颗粒，而不是一条干净的轮廓线。
 *
 * 为什么不给每颗粒子单独随机一个门槛：相邻两颗门槛不同，里面那颗可能被推得比外面那颗还远，
 * 两者互相穿越、叠在一起（实测最近间距能压到 0.003，几乎完全重合）。
 * 起伏只随方位角连续变化，同一方向上的映射保持单调，粒子就不会穿越。
 *
 * 不依赖 three.js 和 DOM，方便脱离渲染单独验证（applySphereField 把结果写进调用方传入的数组）。
 */

/** 一道起伏：k 为绕视线一圈的波数（必须是整数，首尾才接得上），amp 为相对幅度 */
export interface Lump {
  k: number
  amp: number
  speed: number
  phase: number
}

export const FIELD = {
  radius: 1.3, // 空洞基准半径（约 5 个粒子间距，确保一眼看得出是被推开）
  // 影响外缘：超出这个距离的粒子完全不动，保证力场边界连续。
  // 外缘和空洞的比例决定压缩摊得多开：按当前起伏频谱实测，1.45/2.7 时洞边约 24% 的粒子
  // 挤到贴在一起，放宽到 1.3/3.6 降到约 4%（数值随起伏频谱变化，改完跑 field-tune-ratio.ts）。
  reach: 3.6,
  // 低频定大轮廓，中高频出颗粒感。幅度之和就是半径最大起伏比例。
  // k≥8 那两道的幅度之和 × 半径 ≈ 0.16，是粒子间距 0.26 的六成。实测只有三成时边缘看不出参差，
  // 回归脚本的门槛取五成；
  // 再往上加，洞边相邻粒子被剪切得太厉害，开始挤在一起。
  lumps: [
    { k: 3, amp: 0.14, speed: 1.1, phase: 0 },
    { k: 5, amp: 0.08, speed: -0.7, phase: 1.3 },
    { k: 8, amp: 0.07, speed: 1.9, phase: 2.1 },
    { k: 13, amp: 0.05, speed: -2.6, phase: 0.4 },
  ] as Lump[],
  wobble: 0.9, // 起伏随时间变形的速度
  probe: 0.9, // 探测环带表面时，视线周围的取样半径
}

/** 起伏幅度之和：半径最多偏离基准多少 */
function lumpSpan() {
  let span = 0
  for (const l of FIELD.lumps) span += Math.abs(l.amp)
  return span
}

/** 视线：起点 + 单位方向，均为环带局部坐标 */
export interface Ray {
  ox: number
  oy: number
  oz: number
  dx: number
  dy: number
  dz: number
}

/**
 * 视线方向上离相机最近的那层环带表面的深度（沿视线的距离）。
 * 视线附近没有粒子（指向环洞或环外）时，退回到离视线最近那颗粒子的深度，
 * 这样球心始终贴着环带，不会悬在空处。
 */
export function probeSurfaceDepth(
  pos: Float32Array,
  count: number,
  ray: Ray,
  probe: number = FIELD.probe
) {
  const probe2 = probe * probe
  let front = Infinity
  let nearestPerp2 = Infinity
  let nearestAlong = 0
  for (let i = 0; i < count; i++) {
    const vx = pos[i * 3] - ray.ox
    const vy = pos[i * 3 + 1] - ray.oy
    const vz = pos[i * 3 + 2] - ray.oz
    const along = vx * ray.dx + vy * ray.dy + vz * ray.dz
    const perp2 = vx * vx + vy * vy + vz * vz - along * along
    if (perp2 < probe2 && along < front) front = along
    if (perp2 < nearestPerp2) {
      nearestPerp2 = perp2
      nearestAlong = along
    }
  }
  return front < Infinity ? front : nearestAlong
}

/** 球面在方位角 theta 上的半径倍率 */
function shapeAt(theta: number, t: number) {
  let s = 1
  for (const l of FIELD.lumps)
    s += l.amp * Math.sin(l.k * theta + l.speed * t + l.phase)
  return s
}

/**
 * 把 pos 里的粒子按力场推开，结果写进 out（可以和 pos 是同一块内存）。
 *
 * 推法是一个单调的径向重映射：以视线为轴，把 [0, 外缘] 内的粒子压进 [空洞, 外缘] 这个环里，
 * 按面积均匀压缩。于是中心空出来、外缘处位移为零，同一方向上的粒子保持先后顺序、不会互相穿越。
 * 球体体现在深度上：粒子离球心越远（沿视线方向），它所在那一截的横截面就越小。
 *
 * @param depth    球心沿视线的深度，一般取 probeSurfaceDepth 的平滑值
 * @param strength 0~1，指针移入移出时的渐变系数
 * @param time     秒，驱动球面起伏变形
 */
export function applySphereField(
  pos: Float32Array,
  out: Float32Array,
  count: number,
  ray: Ray,
  depth: number,
  strength: number,
  time: number
) {
  if (out !== pos) out.set(pos.subarray(0, count * 3))
  if (strength <= 0.001) return

  // 垂直于视线的一组正交基，用来量粒子绕视线的方位角
  let ux = 0
  let uy = 1
  const uz = 0
  if (Math.abs(ray.dy) > 0.9) {
    ux = 1
    uy = 0
  }
  let e1x = ray.dy * uz - ray.dz * uy
  let e1y = ray.dz * ux - ray.dx * uz
  let e1z = ray.dx * uy - ray.dy * ux
  const e1len = Math.hypot(e1x, e1y, e1z) || 1
  e1x /= e1len
  e1y /= e1len
  e1z /= e1len
  const e2x = ray.dy * e1z - ray.dz * e1y
  const e2y = ray.dz * e1x - ray.dx * e1z
  const e2z = ray.dx * e1y - ray.dy * e1x

  const reachMax = FIELD.reach * (1 + lumpSpan())
  const t = time * FIELD.wobble

  for (let i = 0; i < count; i++) {
    const vx = pos[i * 3] - ray.ox
    const vy = pos[i * 3 + 1] - ray.oy
    const vz = pos[i * 3 + 2] - ray.oz
    const along = vx * ray.dx + vy * ray.dy + vz * ray.dz
    const dz = along - depth // 相对球心的深度差
    if (dz > reachMax || dz < -reachMax) continue

    const px = vx - ray.dx * along
    const py = vy - ray.dy * along
    const pz = vz - ray.dz * along
    const rho2 = px * px + py * py + pz * pz
    if (rho2 > reachMax * reachMax) continue
    const rho = Math.sqrt(rho2)

    // 正好压在视线上的粒子没有方向，按序号给一个固定方位，免得卡在洞中心
    const theta =
      rho > 1e-4
        ? Math.atan2(
            px * e2x + py * e2y + pz * e2z,
            px * e1x + py * e1y + pz * e1z
          )
        : i * 2.399963 // 黄金角，相邻序号方位错开

    const shape = shapeAt(theta, t)
    const rVoid = FIELD.radius * shape * strength
    const rReach = FIELD.reach * shape

    // 这颗粒子所在深度上，球体的横截面半径
    const reach2 = rReach * rReach - dz * dz
    if (reach2 <= 0 || rho2 >= reach2) continue
    const void2 = Math.max(0, rVoid * rVoid - dz * dz)

    const rhoNew = Math.sqrt(void2 + (rho2 * (reach2 - void2)) / reach2)
    const shift = rhoNew - rho

    let nx: number
    let ny: number
    let nz: number
    if (rho > 1e-4) {
      nx = px / rho
      ny = py / rho
      nz = pz / rho
    } else {
      const c = Math.cos(theta)
      const s = Math.sin(theta)
      nx = c * e1x + s * e2x
      ny = c * e1y + s * e2y
      nz = c * e1z + s * e2z
    }
    out[i * 3] += nx * shift
    out[i * 3 + 1] += ny * shift
    out[i * 3 + 2] += nz * shift
  }
}
