import { useEffect, useRef, useState } from 'react'
import { applySphereField, probeSurfaceDepth, type Ray } from './mobius-field'
import { LINK, buildRows, linkFrame, lookup } from './tube-geometry'

/**
 * Hero 主视觉：两根由粒子组成的环管互相穿过（几何见 tube-geometry.ts）。
 *
 * 链环本身不自转，只绕基准姿态缓慢往复；粒子沿管面流动，两环流向相反。
 * 指针处是一颗不规则的颗粒球体，把它碰到的那层管面推开（力场本身见 mobius-field.ts）。
 *
 * 布点和推进都按**弧长**：管面的度规不是常数，按角度等分必然内密外疏。
 * 每条轨道预先积出弧长表，再反查出等弧长处对应的 u；推进用线速度。
 *
 * 关于颜色：每颗小球只有自己的明暗，整根管子是没有明暗的 —— 亮底上白球的灰影还能读出形，
 * 黑底上一团深色小球就成了一坨。所以按**管面法线**给每颗粒子上色（受光面 / 背光面 / 轮廓边缘），
 * 让管子本身有明暗和轮廓光；小球自己的高光仍由灯光给。
 */

interface Palette {
  ambient: [color: number, intensity: number]
  key: [color: number, intensity: number]
  fill: [color: number, intensity: number, position: [number, number, number]]
  /** 按管面法线上色：受光面、背光面、轮廓边缘，以及轮廓光的强度 */
  lit: number
  shadow: number
  rim: number
  rimAmount: number
}

// 亮色：粒子纯白，形靠每颗白球的灰影读出来；不加管面明暗
const LIGHT: Palette = {
  ambient: [0xf1f4f9, 2.3],
  key: [0xffffff, 1.5],
  fill: [0x8f9cb5, 1.15, [7, -5, -6]],
  lit: 0xffffff,
  shadow: 0xffffff,
  rim: 0xffffff,
  rimAmount: 0,
}

// 暗色：管身是克制的灰蓝（受光面亮、背光面沉到接近底色），品牌蓝只留给轮廓边缘和
// 前下方的一点补光 —— 蓝是点缀，不是管子的本色
const DARK: Palette = {
  ambient: [0x3b4766, 2.0],
  key: [0xeef1f6, 1.3],
  fill: [0x3a6fe8, 0.55, [7, -5, 6]],
  lit: 0x8391b5,
  shadow: 0x161c2e,
  rim: 0x5b8af5,
  rimAmount: 1.0,
}

// 主光方向（世界坐标），管面明暗按它算
const KEY_POSITION: [number, number, number] = [-6, 8, 9]

const PARTICLE_RADIUS = 0.09
const FLOW_SPEED = 1.0 // 沿管面前进的线速度（单位/秒），不是角速度

// 朝向：不是匀速自转，而是绕基准姿态缓慢往复。
// 两个周期取互质的数，避免整体动作在短周期内完全重复。
const TILT_BASE = { x: 0.12, y: 0.2, z: 0.1 }
const TILT_AMP = { x: 0.24, y: 0.4 }
const TILT_PERIOD = { x: 19, y: 27 } // 秒

export function ParticleField() {
  const ref = useRef<HTMLDivElement>(null)
  const [dark, setDark] = useState(false)
  const [reduceMotion, setReduceMotion] = useState(false)

  useEffect(() => {
    const root = document.documentElement
    const syncTheme = () => setDark(root.classList.contains('dark'))
    syncTheme()
    const observer = new MutationObserver(syncTheme)
    observer.observe(root, { attributes: true, attributeFilter: ['class'] })

    const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
    const syncMotion = () => setReduceMotion(motion.matches)
    syncMotion()
    motion.addEventListener('change', syncMotion)

    return () => {
      observer.disconnect()
      motion.removeEventListener('change', syncMotion)
    }
  }, [])

  useEffect(() => {
    const host = ref.current
    if (!host) return

    let disposed = false
    let cleanup: (() => void) | undefined

    void (async () => {
      const THREE = await import('three')
      if (disposed || !ref.current) return

      const palette = dark ? DARK : LIGHT
      const scene = new THREE.Scene()

      const camera = new THREE.PerspectiveCamera(
        38,
        host.clientWidth / host.clientHeight,
        0.1,
        100
      )
      camera.position.set(0, 0, 26.5)

      const renderer = new THREE.WebGLRenderer({
        alpha: true,
        antialias: true,
        powerPreference: 'low-power',
      })
      renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
      renderer.setSize(host.clientWidth, host.clientHeight)
      renderer.setClearAlpha(0)
      host.appendChild(renderer.domElement)

      // 两个环共用同一套轨道表，只是放置的位置和流向不同
      const rows = buildRows()
      let perRing = 0
      for (const row of rows) perRing += row.n
      const count = perRing * LINK.flow.length

      const sphere = new THREE.IcosahedronGeometry(PARTICLE_RADIUS, 1)
      const material = new THREE.MeshLambertMaterial({ color: 0xffffff })
      const strip = new THREE.InstancedMesh(sphere, material, count)
      strip.frustumCulled = false
      strip.instanceMatrix.setUsage(THREE.DynamicDrawUsage)
      // 每颗粒子的颜色按管面法线逐帧算，直接写进实例颜色缓冲
      const colors = new Float32Array(count * 3)
      strip.instanceColor = new THREE.InstancedBufferAttribute(colors, 3)
      strip.instanceColor.setUsage(THREE.DynamicDrawUsage)
      // 三个色标转成线性空间，插值在线性空间做
      const lit = new THREE.Color(palette.lit)
      const shadow = new THREE.Color(palette.shadow)
      const rim = new THREE.Color(palette.rim)

      // 每颗粒子只需要记：属于哪个环、哪条轨道、起始归一化弧长
      const ringOf = new Uint8Array(count)
      const rowOf = new Uint16Array(count)
      const startArc = new Float32Array(count)
      {
        let i = 0
        for (let ring = 0; ring < LINK.flow.length; ring++) {
          for (let j = 0; j < rows.length; j++) {
            const { n } = rows[j]
            for (let k = 0; k < n; k++) {
              ringOf[i] = ring
              rowOf[i] = j
              startArc[i] = k / n
              i++
            }
          }
        }
      }

      const group = new THREE.Group()
      group.add(strip)
      group.rotation.set(TILT_BASE.x, TILT_BASE.y, TILT_BASE.z)
      scene.add(group)
      group.updateMatrixWorld(true)
      // 姿态每帧都在摆，局部坐标换算不能只算一次
      const toLocal = group.matrixWorld.clone().invert()

      const ambient = new THREE.AmbientLight(...palette.ambient)
      const key = new THREE.DirectionalLight(...palette.key)
      key.position.set(...KEY_POSITION)
      const fill = new THREE.DirectionalLight(palette.fill[0], palette.fill[1])
      fill.position.set(...palette.fill[2])
      scene.add(ambient, key, fill)

      // 管面明暗用的主光方向和视线方向：世界坐标固定，每帧换算到链环局部坐标
      const keyDir = new THREE.Vector3(...KEY_POSITION).normalize()
      const keyLocal = new THREE.Vector3()
      const viewLocal = new THREE.Vector3()

      const resize = () => {
        if (!ref.current) return
        const w = ref.current.clientWidth
        const h = ref.current.clientHeight
        camera.aspect = w / h
        camera.position.z = w < 768 ? 36 : 26.5
        camera.updateProjectionMatrix()
        renderer.setSize(w, h)
      }
      resize()
      const resizeObserver = new ResizeObserver(resize)
      resizeObserver.observe(host)

      // —— 指针力场 ——
      // 容器是 pointer-events-none，所以监听 window，再换算成容器内坐标。
      //
      // 判据用「粒子到视线射线的垂直距离」，不是「粒子到某个三维点的距离」：
      // 管子是斜的，指针投到世界 z=0 平面得到的点根本不落在管面上，
      // 按点算距离会导致判定半径几乎永远触发不到。垂直距离才等于屏幕上的远近。
      //
      // 指针一律存世界坐标，局部坐标在渲染循环里现算 —— 管子姿态每帧都在摆，
      // 在事件回调里换算会用到上一帧的矩阵，鼠标不动时作用点会跟着姿态漂走。
      const pointerWorld = new THREE.Vector3()
      const pointerWorldTarget = new THREE.Vector3()
      const pointerLocal = new THREE.Vector3()
      const eye = new THREE.Vector3()
      const scratch = new THREE.Vector3()
      let pointerStrength = 0
      let pointerActive = 0
      let pointerSeen = false

      const onPointerMove = (event: PointerEvent) => {
        const rect = host.getBoundingClientRect()
        const inside =
          event.clientX >= rect.left &&
          event.clientX <= rect.right &&
          event.clientY >= rect.top &&
          event.clientY <= rect.bottom
        pointerActive = inside ? 1 : 0
        if (!inside) return
        const nx = ((event.clientX - rect.left) / rect.width) * 2 - 1
        const ny = -((event.clientY - rect.top) / rect.height) * 2 + 1
        scratch
          .set(nx, ny, 0.5)
          .unproject(camera)
          .sub(camera.position)
          .normalize()
        pointerWorldTarget
          .copy(camera.position)
          .addScaledVector(scratch, -camera.position.z / scratch.z)
        if (!pointerSeen) {
          pointerWorld.copy(pointerWorldTarget)
          pointerSeen = true
        }
      }
      const onPointerLeave = () => {
        pointerActive = 0
      }
      if (!reduceMotion) {
        window.addEventListener('pointermove', onPointerMove, { passive: true })
        window.addEventListener('pointerleave', onPointerLeave)
        window.addEventListener('blur', onPointerLeave)
      }

      let onScreen = true
      const visibility = new IntersectionObserver(
        ([entry]) => {
          onScreen = entry.isIntersecting
        },
        { threshold: 0 }
      )
      visibility.observe(host)

      // 粒子大小一致，缩放已烘进几何体，循环里只改位移
      const matrix = new THREE.Matrix4()
      const basePos = new Float32Array(count * 3)
      const normal = new Float32Array(3)
      const fieldPos = new Float32Array(count * 3)
      const ray: Ray = { ox: 0, oy: 0, oz: 0, dx: 0, dy: 0, dz: 1 }
      let fieldDepth = 0 // 球心沿视线的深度（平滑后）
      let depthSeen = false
      let elapsed = 0

      const writeInstances = (dt = 0) => {
        // 第一趟：所有粒子在管面上的基准位置，顺手按法线算颜色
        keyLocal.copy(keyDir).transformDirection(toLocal)
        viewLocal.set(0, 0, 1).transformDirection(toLocal)
        for (let i = 0; i < count; i++) {
          const ring = ringOf[i]
          const row = rows[rowOf[i]]
          // 按弧长推进，再反查出 u —— 等弧长才是等间距
          let s =
            startArc[i] + (elapsed * FLOW_SPEED * LINK.flow[ring]) / row.total
          s -= Math.floor(s)
          linkFrame(
            ring,
            lookup(row.lut, s),
            row.phi,
            basePos,
            i * 3,
            normal,
            0
          )

          // 包裹式漫反射：背光面也留一点过渡，不是硬切成两半
          const nl =
            normal[0] * keyLocal.x +
            normal[1] * keyLocal.y +
            normal[2] * keyLocal.z
          const d = 0.5 + 0.5 * nl
          // 轮廓光：法线越垂直于视线越强，只在管子的边缘出现
          const nv = Math.max(
            0,
            normal[0] * viewLocal.x +
              normal[1] * viewLocal.y +
              normal[2] * viewLocal.z
          )
          const f = (1 - nv) * (1 - nv) * (1 - nv) * palette.rimAmount

          const c = i * 3
          const r = shadow.r + (lit.r - shadow.r) * d
          const g = shadow.g + (lit.g - shadow.g) * d
          const b = shadow.b + (lit.b - shadow.b) * d
          colors[c] = r + (rim.r - r) * f
          colors[c + 1] = g + (rim.g - g) * f
          colors[c + 2] = b + (rim.b - b) * f
        }
        strip.instanceColor!.needsUpdate = true

        // 第二趟：指针处的颗粒球把碰到的那层推开
        let final = basePos
        if (pointerStrength > 0.001) {
          eye.copy(camera.position).applyMatrix4(toLocal)
          pointerLocal.copy(pointerWorld).applyMatrix4(toLocal)
          const dx = pointerLocal.x - eye.x
          const dy = pointerLocal.y - eye.y
          const dz = pointerLocal.z - eye.z
          const len = Math.hypot(dx, dy, dz) || 1
          ray.ox = eye.x
          ray.oy = eye.y
          ray.oz = eye.z
          ray.dx = dx / len
          ray.dy = dy / len
          ray.dz = dz / len

          // 用推开前的位置探测表面深度，否则推出的空洞会让下一帧探测落空
          const target = probeSurfaceDepth(basePos, count, ray)
          fieldDepth = depthSeen
            ? fieldDepth + (target - fieldDepth) * (1 - Math.pow(0.002, dt))
            : target
          depthSeen = true

          applySphereField(
            basePos,
            fieldPos,
            count,
            ray,
            fieldDepth,
            pointerStrength,
            elapsed
          )
          final = fieldPos
        } else {
          depthSeen = false // 下次移入时直接落到新位置，不从上次的深度滑过来
        }

        for (let i = 0; i < count; i++) {
          matrix.setPosition(final[i * 3], final[i * 3 + 1], final[i * 3 + 2])
          strip.setMatrixAt(i, matrix)
        }
        strip.instanceMatrix.needsUpdate = true
      }

      let frame = 0
      const clock = new THREE.Clock()

      if (reduceMotion) {
        // 不放动效时给一帧静态画面，而不是整块空白
        writeInstances()
        renderer.render(scene, camera)
      } else {
        const render = () => {
          frame = requestAnimationFrame(render)
          const dt = Math.min(clock.getDelta(), 0.05)
          if (!onScreen) return
          elapsed += dt

          // 朝向缓慢往复，不是匀速自转
          group.rotation.x =
            TILT_BASE.x +
            Math.sin((elapsed / TILT_PERIOD.x) * Math.PI * 2) * TILT_AMP.x
          group.rotation.y =
            TILT_BASE.y +
            Math.sin((elapsed / TILT_PERIOD.y) * Math.PI * 2) * TILT_AMP.y
          group.updateMatrixWorld(true)
          toLocal.copy(group.matrixWorld).invert()

          pointerWorld.lerp(pointerWorldTarget, 1 - Math.pow(0.001, dt))
          pointerStrength +=
            (pointerActive - pointerStrength) * (1 - Math.pow(0.02, dt))
          writeInstances(dt)
          renderer.render(scene, camera)
        }
        render()
      }

      cleanup = () => {
        cancelAnimationFrame(frame)
        resizeObserver.disconnect()
        visibility.disconnect()
        window.removeEventListener('pointermove', onPointerMove)
        window.removeEventListener('pointerleave', onPointerLeave)
        window.removeEventListener('blur', onPointerLeave)
        renderer.domElement.remove()
        renderer.dispose()
        sphere.dispose()
        material.dispose()
      }
    })()

    return () => {
      disposed = true
      cleanup?.()
    }
  }, [dark, reduceMotion])

  return (
    <div
      ref={ref}
      aria-hidden
      className='pointer-events-none absolute inset-0 [&>canvas]:!h-full [&>canvas]:!w-full'
    />
  )
}
