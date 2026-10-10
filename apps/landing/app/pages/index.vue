<script setup lang="ts">
usePageSeo('home', 'meta.homeTitle', 'meta.homeDescription')
useStructuredData()
</script>

<template>
  <div>
    <HeroSection />
    <ProblemSection />
    <FeaturesSection />
    <HowSection />
    <ReliabilitySection />
    <SecuritySection />
    <PricingSection />
    <DemoSection />
    <FaqSection />
    <FloatingCta />
  </div>
</template>
