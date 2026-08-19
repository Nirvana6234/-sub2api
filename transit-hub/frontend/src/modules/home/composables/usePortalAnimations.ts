import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import type { Ref } from 'vue'

gsap.registerPlugin(ScrollTrigger)

export function usePortalAnimations() {
  const playHeroEntrance = (refs: {
    badge: Ref<HTMLElement | null>,
    title: Ref<HTMLElement | null>,
    subtitle: Ref<HTMLElement | null>,
    actions: Ref<HTMLElement | null>,
    visual: Ref<HTMLElement | null>
  }) => {
    const tl = gsap.timeline({ defaults: { ease: 'power4.out' } })
    
    if (refs.badge.value) {
      tl.fromTo(refs.badge.value, 
        { y: 30, opacity: 0, scale: 0.9 }, 
        { y: 0, opacity: 1, scale: 1, duration: 0.8 }
      )
    }
    
    if (refs.title.value) {
      tl.fromTo(refs.title.value,
        { y: 60, opacity: 0, rotationX: 15 },
        { y: 0, opacity: 1, rotationX: 0, duration: 1.2, transformPerspective: 800 },
        '-=0.5'
      )
    }
    
    if (refs.subtitle.value) {
      tl.fromTo(refs.subtitle.value,
        { y: 30, opacity: 0 },
        { y: 0, opacity: 1, duration: 0.8 },
        '-=0.8'
      )
    }
    
    if (refs.actions.value) {
      tl.fromTo(refs.actions.value,
        { y: 20, opacity: 0 },
        { y: 0, opacity: 1, duration: 0.6 },
        '-=0.6'
      )
    }
    
    if (refs.visual.value) {
      tl.fromTo(refs.visual.value,
        { scale: 0.9, opacity: 0, y: 50 },
        { scale: 1, opacity: 1, y: 0, duration: 1.5, ease: 'expo.out' },
        '-=1'
      )
      
      gsap.to(refs.visual.value, {
        y: -15,
        duration: 3,
        ease: 'sine.inOut',
        yoyo: true,
        repeat: -1,
        delay: 0.5
      })
    }
    
    return tl
  }

  const setupScrollAnimations = (
    featuresContainer: Ref<HTMLElement | null>,
    featureCards: Ref<HTMLElement[]>,
    ctaContainer: Ref<HTMLElement | null>
  ) => {
    if (featuresContainer.value && featureCards.value.length > 0) {
      gsap.fromTo(featureCards.value,
        { y: 80, opacity: 0, scale: 0.9 },
        {
          y: 0,
          opacity: 1,
          scale: 1,
          duration: 0.8,
          stagger: 0.15,
          ease: 'back.out(1.2)',
          scrollTrigger: {
            trigger: featuresContainer.value,
            start: 'top 80%',
            toggleActions: 'play none none reverse'
          }
        }
      )
    }

    if (ctaContainer.value) {
      gsap.fromTo(ctaContainer.value,
        { opacity: 0, scale: 0.95, y: 40 },
        {
          opacity: 1,
          scale: 1,
          y: 0,
          duration: 1,
          ease: 'power3.out',
          scrollTrigger: {
            trigger: ctaContainer.value,
            start: 'top 85%',
            toggleActions: 'play none none reverse'
          }
        }
      )
    }
  }

  return { playHeroEntrance, setupScrollAnimations }
}
