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

<canvas class="block h-full min-h-0 w-full bg-[#073c3b]" bind:this={canvas} width="1280" height="800" aria-label={`Beemo face: ${expression}`}></canvas>
