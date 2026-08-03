<script lang="ts">
  import { onMount } from "svelte";
  import { mountFace, type FaceController } from "$lib/face-engine";
  import type { FaceExpression } from "$lib/types";

  export let expression: FaceExpression = "calm";
  export let talking = false;

  let canvas: HTMLCanvasElement;
  let controller: FaceController | undefined;

  onMount(() => {
    controller = mountFace(canvas, "/assets/display-texture.jpg");
    controller.setExpression(expression);
    controller.setTalking(talking);
    return () => controller?.destroy();
  });

  $: controller?.setExpression(expression);
  $: controller?.setTalking(talking);
</script>

<canvas bind:this={canvas} width="1280" height="800" aria-label={`Beemo face: ${expression}`}></canvas>

<style>
  canvas {
    display: block;
    width: 100%;
    height: 100%;
    min-height: 0;
    background: #073c3b;
  }
</style>
