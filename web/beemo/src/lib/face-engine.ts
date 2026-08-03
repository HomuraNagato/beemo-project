import type { FaceExpression } from "$lib/types";

interface FaceShape {
  eyeY: number;
  eyeScaleX: number;
  eyeScaleY: number;
  browLiftLeft: number;
  browLiftRight: number;
  browTiltLeft: number;
  browTiltRight: number;
  browWeight: number;
  mouthCurve: number;
  mouthOpen: number;
  mouthWidth: number;
  mouthY: number;
  glow: number;
  pulse: number;
  scanSpeed: number;
}

export interface FaceController {
  destroy(): void;
  expression(): FaceExpression;
  setExpression(expression: FaceExpression): void;
  setTalking(talking: boolean): void;
}

const states: Record<FaceExpression, FaceShape> = {
  calm: shape({ mouthCurve: 0.55, mouthWidth: 0.29, mouthY: 0.48, glow: 0.45 }),
  sad: shape({ eyeScaleY: 0.94, browLiftLeft: 0.018, browLiftRight: 0.018, browTiltLeft: -0.055, browTiltRight: 0.055, browWeight: 0.72, mouthCurve: -0.54, mouthWidth: 0.28, mouthY: 0.54, glow: 0.25, pulse: 0.08, scanSpeed: 0.7 }),
  worried: shape({ eyeScaleX: 0.96, eyeScaleY: 1.02, browLiftLeft: 0.03, browLiftRight: 0.03, browTiltLeft: -0.105, browTiltRight: 0.105, browWeight: 1, mouthCurve: -0.72, mouthWidth: 0.255, mouthY: 0.54, glow: 0.32, pulse: 0.12, scanSpeed: 1.3 }),
  surprised: shape({ eyeY: 0.305, eyeScaleX: 1.03, eyeScaleY: 1.08, browLiftLeft: 0.055, browLiftRight: 0.055, browWeight: 0.58, mouthCurve: 0, mouthOpen: 0.78, mouthWidth: 0.16, mouthY: 0.515, glow: 0.62, pulse: 0.28, scanSpeed: 1.8 }),
  listening: shape({ eyeScaleX: 0.96, eyeScaleY: 0.84, browLiftLeft: 0.024, browLiftRight: 0.024, browWeight: 0.46, mouthCurve: 0.03, mouthWidth: 0.22, mouthY: 0.52, glow: 0.38, pulse: 0.2, scanSpeed: 0.9 }),
  thinking: shape({ eyeY: 0.3, eyeScaleX: 0.78, eyeScaleY: 0.82, browLiftLeft: 0.065, browLiftRight: -0.025, browTiltLeft: -0.035, browTiltRight: 0.025, browWeight: 1, mouthCurve: 0.04, mouthWidth: 0.2, mouthY: 0.525, glow: 0.5, pulse: 0.35, scanSpeed: 2.2 }),
  hidden: shape({ eyeScaleX: 0, eyeScaleY: 0, mouthCurve: 0, mouthWidth: 0, glow: 0.18, pulse: 0.05, scanSpeed: 0.5 }),
};

function shape(overrides: Partial<FaceShape>): FaceShape {
  return {
    eyeY: 0.31,
    eyeScaleX: 1,
    eyeScaleY: 1,
    browLiftLeft: 0,
    browLiftRight: 0,
    browTiltLeft: 0,
    browTiltRight: 0,
    browWeight: 0,
    mouthCurve: 0,
    mouthOpen: 0,
    mouthWidth: 0.24,
    mouthY: 0.52,
    glow: 0.4,
    pulse: 0.15,
    scanSpeed: 1,
    ...overrides,
  };
}

export function mountFace(canvas: HTMLCanvasElement, textureUrl: string): FaceController {
  const context = canvasContext(canvas);

  const texture = new Image();
  texture.src = textureUrl;
  let expression: FaceExpression = "calm";
  let active = { ...states.calm };
  let from = { ...states.calm };
  let target = { ...states.calm };
  let transitionStart = performance.now();
  let talking = false;
  let talkingLevel = 0;
  let talkingFrom = 0;
  let talkingTarget = 0;
  let talkingStart = performance.now();
  let speechOpen = 0;
  let speechTarget = 0;
  let nextSpeechChange = 0;
  let blinkStart = -1;
  let nextBlink = performance.now() + 2400;
  let frame = 0;
  let stopped = false;

  function resize(): DOMRect {
    const bounds = canvas.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const width = Math.max(1, Math.round(bounds.width * dpr));
    const height = Math.max(1, Math.round(bounds.height * dpr));
    if (canvas.width !== width || canvas.height !== height) {
      canvas.width = width;
      canvas.height = height;
    }
    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    return bounds;
  }

  function drawTexture(width: number, height: number, time: number): void {
    if (texture.complete && texture.naturalWidth > 0) {
      const imageRatio = texture.naturalWidth / texture.naturalHeight;
      const canvasRatio = width / height;
      let drawWidth = width;
      let drawHeight = height;
      let x = 0;
      let y = 0;
      if (canvasRatio > imageRatio) {
        drawHeight = width / imageRatio;
        y = (height - drawHeight) / 2;
      } else {
        drawWidth = height * imageRatio;
        x = (width - drawWidth) / 2;
      }
      context.drawImage(texture, x, y, drawWidth, drawHeight);
    } else {
      context.fillStyle = "#073c3b";
      context.fillRect(0, 0, width, height);
    }

    const shimmer = context.createRadialGradient(width * 0.48, height * 0.4, height * 0.05, width * 0.48, height * 0.4, height * 0.7);
    shimmer.addColorStop(0, `rgba(40, 210, 177, ${0.18 + active.glow * 0.16})`);
    shimmer.addColorStop(0.55, "rgba(9, 61, 64, 0.05)");
    shimmer.addColorStop(1, "rgba(2, 10, 18, 0.34)");
    context.fillStyle = shimmer;
    context.fillRect(0, 0, width, height);
    const scanY = ((time * 0.04 * active.scanSpeed) % (height + 120)) - 60;
    context.fillStyle = `rgba(236, 210, 106, ${0.04 + active.pulse * 0.08})`;
    context.fillRect(0, scanY, width, 2);
  }

  function drawEye(x: number, y: number, radius: number, blink: number): void {
    context.save();
    context.translate(x, y);
    context.scale(Math.max(active.eyeScaleX, 0.001), Math.max(active.eyeScaleY * blink, 0.001));
    context.beginPath();
    context.arc(0, 0, radius, 0, Math.PI * 2);
    context.fillStyle = "rgba(0, 6, 14, 0.72)";
    context.fill();
    context.restore();
  }

  function drawBrow(x: number, y: number, width: number, lift: number, tilt: number, height: number): void {
    if (active.browWeight < 0.01) return;
    context.save();
    context.translate(x, y - lift * height);
    context.rotate(tilt);
    context.lineCap = "round";
    context.lineWidth = Math.max(8, width * 0.1);
    context.strokeStyle = "rgba(0, 6, 14, 0.56)";
    context.beginPath();
    context.moveTo(-width * 0.45, 0);
    context.lineTo(width * 0.45, 0);
    context.stroke();
    context.restore();
  }

  function drawMouth(width: number, height: number): void {
    const mouthY = lerp(active.mouthY, 0.515, talkingLevel * 0.6);
    const mouthWidth = width * lerp(active.mouthWidth, Math.max(0.19, active.mouthWidth * 0.82), talkingLevel);
    const open = lerp(active.mouthOpen, active.mouthOpen * 0.35 + 0.08 + speechOpen * 0.58, talkingLevel);
    const curve = height * active.mouthCurve * 0.18 * (1 - talkingLevel * 0.78);
    const halfWidth = mouthWidth * 0.5;
    if (halfWidth < 0.5) return;
    const openHeight = height * 0.052 * open;
    const shapeMix = smoothstep(0.015, 0.22, open);
    const controlX = lerp(halfWidth * 0.34, halfWidth, shapeMix);

    context.save();
    context.translate(width * 0.5, height * mouthY);
    context.strokeStyle = "rgba(0, 6, 14, 0.7)";
    context.lineCap = "round";
    context.lineJoin = "round";
    context.lineWidth = Math.max(10, width * lerp(0.022, 0.0155, Math.max(talkingLevel, shapeMix)));
    context.beginPath();
    context.moveTo(-halfWidth, 0);
    context.bezierCurveTo(-controlX, curve * 0.67 - openHeight * 1.32, controlX, curve * 0.67 - openHeight * 1.32, halfWidth, 0);
    context.bezierCurveTo(controlX, curve * 0.67 + openHeight * 1.32, -controlX, curve * 0.67 + openHeight * 1.32, -halfWidth, 0);
    context.stroke();
    context.restore();
  }

  function render(time: number): void {
    if (stopped) return;
    const bounds = resize();
    active = interpolate(from, target, Math.min((time - transitionStart) / 850, 1));
    talkingLevel = lerp(talkingFrom, talkingTarget, ease(Math.min((time - talkingStart) / 320, 1)));
    if (talking && time >= nextSpeechChange) {
      speechTarget = speechTarget > 0 ? 0 : random(0.68, 1);
      nextSpeechChange = time + (speechTarget > 0 ? random(210, 310) : random(90, 250));
    }
    speechOpen = lerp(speechOpen, talking ? speechTarget : 0, 0.18);
    if (time > nextBlink) {
      blinkStart = time;
      nextBlink = time + random(2400, 5000);
    }
    const blinkProgress = blinkStart < 0 ? 1 : Math.min((time - blinkStart) / 190, 1);
    const blink = blinkStart < 0 ? 1 : 1 - Math.sin(Math.PI * blinkProgress) ** 2 * 0.9;
    if (blinkProgress >= 1) blinkStart = -1;

    context.clearRect(0, 0, bounds.width, bounds.height);
    drawTexture(bounds.width, bounds.height, time);
    const radius = Math.min(bounds.width, bounds.height) * 0.085;
    const eyeY = bounds.height * active.eyeY;
    const leftX = bounds.width * 0.32;
    const rightX = bounds.width * 0.68;
    drawEye(leftX, eyeY, radius, blink);
    drawEye(rightX, eyeY, radius, blink);
    drawBrow(leftX, eyeY - radius * 1.15, radius * 1.35, active.browLiftLeft, active.browTiltLeft, bounds.height);
    drawBrow(rightX, eyeY - radius * 1.15, radius * 1.35, active.browLiftRight, active.browTiltRight, bounds.height);
    drawMouth(bounds.width, bounds.height);
    frame = requestAnimationFrame(render);
  }

  frame = requestAnimationFrame(render);
  return {
    destroy() {
      stopped = true;
      cancelAnimationFrame(frame);
    },
    expression() {
      return expression;
    },
    setExpression(next) {
      if (next === expression) return;
      from = { ...active };
      target = { ...states[next] };
      expression = next;
      transitionStart = performance.now();
      if (next === "hidden") this.setTalking(false);
    },
    setTalking(next) {
      const enabled = next && expression !== "hidden";
      if (enabled === talking) return;
      talking = enabled;
      talkingFrom = talkingLevel;
      talkingTarget = enabled ? 1 : 0;
      talkingStart = performance.now();
      nextSpeechChange = talkingStart;
    },
  };
}

function canvasContext(canvas: HTMLCanvasElement): CanvasRenderingContext2D {
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas 2D is unavailable");
  return context;
}

function interpolate(from: FaceShape, to: FaceShape, amount: number): FaceShape {
  const eased = ease(amount);
  return Object.fromEntries(Object.keys(from).map((key) => {
    const name = key as keyof FaceShape;
    return [name, lerp(from[name], to[name], eased)];
  })) as unknown as FaceShape;
}

function ease(value: number): number {
  return value < 0.5 ? 4 * value ** 3 : 1 - (-2 * value + 2) ** 3 / 2;
}

function lerp(from: number, to: number, amount: number): number {
  return from + (to - from) * amount;
}

function smoothstep(from: number, to: number, value: number): number {
  const amount = Math.min(Math.max((value - from) / (to - from), 0), 1);
  return amount * amount * (3 - 2 * amount);
}

function random(minimum: number, maximum: number): number {
  return minimum + Math.random() * (maximum - minimum);
}
