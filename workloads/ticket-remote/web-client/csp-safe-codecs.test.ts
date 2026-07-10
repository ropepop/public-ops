import assert from "node:assert/strict";
import test from "node:test";
import {
  AlgebraicType,
  Timestamp,
  type AlgebraicTypeType,
  type ProductTypeType,
} from "spacetimedb";
import {
  cspSafeCodecRoundTrip,
  installCspSafeSpacetimeCodecs,
} from "./src/csp-safe-codecs.ts";

function withFunctionConstructorBlocked(run: () => void): void {
  const originalFunction = globalThis.Function;
  const blockedFunction = new Proxy(originalFunction, {
    apply() {
      throw new EvalError("Function constructor blocked by strict CSP");
    },
    construct() {
      throw new EvalError("Function constructor blocked by strict CSP");
    },
  });
  Object.defineProperty(globalThis, "Function", {
    configurable: true,
    value: blockedFunction,
    writable: true,
  });
  try {
    run();
  } finally {
    Object.defineProperty(globalThis, "Function", {
      configurable: true,
      value: originalFunction,
      writable: true,
    });
  }
}

test("closure codecs round-trip product, sum, option, result, unit, and special products under strict CSP", () => {
  withFunctionConstructorBlocked(() => {
    installCspSafeSpacetimeCodecs();

    const recordType = AlgebraicType.Product({
      elements: [
        { name: "name", algebraicType: AlgebraicType.String },
        { name: "count", algebraicType: AlgebraicType.U64 },
        { name: "enabled", algebraicType: AlgebraicType.Bool },
      ],
    });
    assert.deepEqual(cspSafeCodecRoundTrip(recordType, {
      name: "ticket",
      count: 42n,
      enabled: true,
    }), {
      name: "ticket",
      count: 42n,
      enabled: true,
    });

    const sumType = AlgebraicType.Sum({
      variants: [
        { name: "number", algebraicType: AlgebraicType.U32 },
        { name: "text", algebraicType: AlgebraicType.String },
      ],
    });
    assert.deepEqual(cspSafeCodecRoundTrip(sumType, { tag: "text", value: "ready" }), {
      tag: "text",
      value: "ready",
    });

    const unitType = AlgebraicType.Product({ elements: [] });
    assert.deepEqual(cspSafeCodecRoundTrip(unitType, {}), {});

    const optionType = AlgebraicType.Sum({
      variants: [
        { name: "some", algebraicType: AlgebraicType.String },
        { name: "none", algebraicType: unitType },
      ],
    });
    assert.equal(cspSafeCodecRoundTrip(optionType, "available"), "available");
    assert.equal(cspSafeCodecRoundTrip(optionType, undefined), undefined);

    const resultType = AlgebraicType.Sum({
      variants: [
        { name: "ok", algebraicType: AlgebraicType.U32 },
        { name: "err", algebraicType: AlgebraicType.String },
      ],
    });
    assert.deepEqual(cspSafeCodecRoundTrip(resultType, { ok: 7 }), { ok: 7 });
    assert.deepEqual(cspSafeCodecRoundTrip(resultType, { err: "not ready" }), { err: "not ready" });

    const timestamp = cspSafeCodecRoundTrip(Timestamp.getAlgebraicType(), new Timestamp(1234567n));
    assert.ok(timestamp instanceof Timestamp);
    assert.equal(timestamp.microsSinceUnixEpoch, 1234567n);
  });
});

test("closure codecs support recursive product and option references", () => {
  withFunctionConstructorBlocked(() => {
    installCspSafeSpacetimeCodecs();

    const unitType = AlgebraicType.Product({ elements: [] });
    const nodeProduct: ProductTypeType = { elements: [] };
    const nodeType: AlgebraicTypeType = AlgebraicType.Product(nodeProduct);
    nodeProduct.elements.push(
      { name: "value", algebraicType: AlgebraicType.U32 },
      {
        name: "next",
        algebraicType: AlgebraicType.Sum({
          variants: [
            { name: "some", algebraicType: AlgebraicType.Ref(0) },
            { name: "none", algebraicType: unitType },
          ],
        }),
      }
    );
    const typespace = { types: [nodeType] };
    const value = {
      value: 1,
      next: {
        value: 2,
        next: undefined,
      },
    };
    assert.deepEqual(cspSafeCodecRoundTrip(nodeType, value, typespace), value);
  });
});
