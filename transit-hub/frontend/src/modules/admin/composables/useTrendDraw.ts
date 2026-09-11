// 趋势图的 GSAP 入场动画：折线沿路径“画”出来，面积渐显。
// 按前端规范，GSAP 逻辑集中在模块自己的 composable 里，不散落在组件模板中。

import { onMounted, watch, nextTick, type Ref } from 'vue'
import gsap from 'gsap'

/**
 * @param pathRef  折线 <path> 元素引用
 * @param areaRef  面积 <path> 元素引用
 * @param dep      依赖 getter（路径字符串），变化时重放动画
 */
export function useTrendDraw(
  pathRef: Ref<SVGPathElement | null>,
  areaRef: Ref<SVGPathElement | null>,
  dep: () => string,
): void {
  const play = async () => {
    await nextTick()
    const path = pathRef.value
    if (path && typeof path.getTotalLength === 'function') {
      const length = path.getTotalLength()
      if (length > 0) {
        gsap.fromTo(
          path,
          { strokeDasharray: length, strokeDashoffset: length },
          { strokeDashoffset: 0, duration: 0.9, ease: 'power2.out' },
        )
      }
    }
    if (areaRef.value) {
      gsap.fromTo(areaRef.value, { opacity: 0 }, { opacity: 1, duration: 0.9, ease: 'power1.out' })
    }
  }

  onMounted(play)
  watch(dep, play)
}
