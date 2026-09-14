<script lang="ts">
  export let content: string;

  type Segment = { value: string; url?: string };

  function linkify(value: string): Segment[] {
    const segments: Segment[] = [];
    const pattern = /https?:\/\/[^\s<>]+/g;
    let offset = 0;
    for (const match of value.matchAll(pattern)) {
      const index = match.index || 0;
      if (index > offset) segments.push({ value: value.slice(offset, index) });
      const raw = match[0];
      const url = raw.replace(/[.,;:!?]+$/, "");
      segments.push({ value: url, url });
      if (url.length < raw.length) segments.push({ value: raw.slice(url.length) });
      offset = index + raw.length;
    }
    if (offset < value.length) segments.push({ value: value.slice(offset) });
    return segments;
  }

  $: segments = linkify(content);
</script>

{#each segments as segment}
  {#if segment.url}
    <a class="text-beemo-mint underline decoration-[rgba(102,217,180,0.45)] underline-offset-2 hover:decoration-beemo-mint" href={segment.url} target="_blank" rel="noopener noreferrer">{segment.value}</a>
  {:else}{segment.value}{/if}
{/each}
