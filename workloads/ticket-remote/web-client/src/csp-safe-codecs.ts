import {
  AlgebraicType,
  ConnectionId,
  Identity,
  ProductType,
  SumType,
  TimeDuration,
  Timestamp,
  Uuid,
  type AlgebraicTypeType,
  type Deserializer,
  type ProductTypeType,
  type Serializer,
  type SumTypeType,
} from "spacetimedb";

type Typespace = { types: AlgebraicTypeType[] };

const installedMarker = Symbol.for("ticket-remote.spacetimedb.csp-safe-codecs");
const productSerializers = new WeakMap<ProductTypeType, Serializer<any>>();
const productDeserializers = new WeakMap<ProductTypeType, Deserializer<any>>();
const sumSerializers = new WeakMap<SumTypeType, Serializer<any>>();
const sumDeserializers = new WeakMap<SumTypeType, Deserializer<any>>();

const specialProductDeserializers: Record<string, Deserializer<any>> = {
  __time_duration_micros__: (reader) => new TimeDuration(reader.readI64()),
  __timestamp_micros_since_unix_epoch__: (reader) => new Timestamp(reader.readI64()),
  __identity__: (reader) => new Identity(reader.readU256()),
  __connection_id__: (reader) => new ConnectionId(reader.readU128()),
  __uuid__: (reader) => new Uuid(reader.readU128()),
};

function productSerializer(ty: ProductTypeType, typespace?: Typespace): Serializer<any> {
  const cached = productSerializers.get(ty);
  if (cached) return cached;

  let fields: Array<{ name: string; serialize: Serializer<any> }> | undefined;
  const serialize: Serializer<any> = (writer, value) => {
    if (!fields) throw new Error("recursive product serializer was used before initialization");
    for (const field of fields) {
      field.serialize(writer, value[field.name]);
    }
  };
  productSerializers.set(ty, serialize);

  try {
    fields = ty.elements.map((element) => ({
      name: String(element.name),
      serialize: AlgebraicType.makeSerializer(element.algebraicType, typespace),
    }));
  } catch (error) {
    productSerializers.delete(ty);
    throw error;
  }
  return serialize;
}

function productDeserializer(ty: ProductTypeType, typespace?: Typespace): Deserializer<any> {
  if (ty.elements.length === 0) return () => ({});
  if (ty.elements.length === 1) {
    const special = specialProductDeserializers[String(ty.elements[0].name)];
    if (special) return special;
  }

  const cached = productDeserializers.get(ty);
  if (cached) return cached;

  let fields: Array<{ name: string; deserialize: Deserializer<any> }> | undefined;
  const deserialize: Deserializer<any> = (reader) => {
    if (!fields) throw new Error("recursive product deserializer was used before initialization");
    const result: Record<string, unknown> = {};
    for (const field of fields) {
      result[field.name] = field.deserialize(reader);
    }
    return result;
  };
  productDeserializers.set(ty, deserialize);

  try {
    fields = ty.elements.map((element) => ({
      name: String(element.name),
      deserialize: AlgebraicType.makeDeserializer(element.algebraicType, typespace),
    }));
  } catch (error) {
    productDeserializers.delete(ty);
    throw error;
  }
  return deserialize;
}

function isOption(ty: SumTypeType): boolean {
  return ty.variants.length === 2 && ty.variants[0].name === "some" && ty.variants[1].name === "none";
}

function isResult(ty: SumTypeType): boolean {
  return ty.variants.length === 2 && ty.variants[0].name === "ok" && ty.variants[1].name === "err";
}

function sumSerializer(ty: SumTypeType, typespace?: Typespace): Serializer<any> {
  if (isOption(ty)) {
    const serializeSome = AlgebraicType.makeSerializer(ty.variants[0].algebraicType, typespace);
    return (writer, value) => {
      if (value !== null && value !== undefined) {
        writer.writeU8(0);
        serializeSome(writer, value);
      } else {
        writer.writeU8(1);
      }
    };
  }

  if (isResult(ty)) {
    const serializeOk = AlgebraicType.makeSerializer(ty.variants[0].algebraicType, typespace);
    const serializeErr = AlgebraicType.makeSerializer(ty.variants[1].algebraicType, typespace);
    return (writer, value) => {
      if (value != null && "ok" in Object(value)) {
        writer.writeU8(0);
        serializeOk(writer, value.ok);
      } else if (value != null && "err" in Object(value)) {
        writer.writeU8(1);
        serializeErr(writer, value.err);
      } else {
        throw new TypeError("could not serialize result: object had neither an `ok` nor an `err` field");
      }
    };
  }

  const cached = sumSerializers.get(ty);
  if (cached) return cached;

  let variants: Map<string | undefined, { index: number; serialize: Serializer<any> }> | undefined;
  const serialize: Serializer<any> = (writer, value) => {
    if (!variants) throw new Error("recursive sum serializer was used before initialization");
    const variant = variants.get(value && value.tag);
    if (!variant) throw new TypeError(`Could not serialize sum type; unknown tag ${value && value.tag}`);
    writer.writeU8(variant.index);
    variant.serialize(writer, value.value);
  };
  sumSerializers.set(ty, serialize);

  try {
    variants = new Map(
      ty.variants.map((variant, index) => [
        variant.name,
        {
          index,
          serialize: AlgebraicType.makeSerializer(variant.algebraicType, typespace),
        },
      ])
    );
  } catch (error) {
    sumSerializers.delete(ty);
    throw error;
  }
  return serialize;
}

function sumDeserializer(ty: SumTypeType, typespace?: Typespace): Deserializer<any> {
  if (isOption(ty)) {
    const deserializeSome = AlgebraicType.makeDeserializer(ty.variants[0].algebraicType, typespace);
    return (reader) => {
      const tag = reader.readU8();
      if (tag === 0) return deserializeSome(reader);
      if (tag === 1) return undefined;
      throw new TypeError(`Can't deserialize an option type, couldn't find ${tag} tag`);
    };
  }

  if (isResult(ty)) {
    const deserializeOk = AlgebraicType.makeDeserializer(ty.variants[0].algebraicType, typespace);
    const deserializeErr = AlgebraicType.makeDeserializer(ty.variants[1].algebraicType, typespace);
    return (reader) => {
      const tag = reader.readU8();
      if (tag === 0) return { ok: deserializeOk(reader) };
      if (tag === 1) return { err: deserializeErr(reader) };
      throw new TypeError(`Can't deserialize a result type, couldn't find ${tag} tag`);
    };
  }

  const cached = sumDeserializers.get(ty);
  if (cached) return cached;

  let variants: Array<{ name: string | undefined; deserialize: Deserializer<any> }> | undefined;
  const deserialize: Deserializer<any> = (reader) => {
    if (!variants) throw new Error("recursive sum deserializer was used before initialization");
    const variant = variants[reader.readU8()];
    if (!variant) return undefined;
    return { tag: variant.name, value: variant.deserialize(reader) };
  };
  sumDeserializers.set(ty, deserialize);

  try {
    variants = ty.variants.map((variant) => ({
      name: variant.name,
      deserialize: AlgebraicType.makeDeserializer(variant.algebraicType, typespace),
    }));
  } catch (error) {
    sumDeserializers.delete(ty);
    throw error;
  }
  return deserialize;
}

export function installCspSafeSpacetimeCodecs(): void {
  const product = ProductType as typeof ProductType & { [installedMarker]?: boolean };
  if (product[installedMarker]) return;

  ProductType.makeSerializer = productSerializer;
  ProductType.makeDeserializer = productDeserializer;
  SumType.makeSerializer = sumSerializer;
  SumType.makeDeserializer = sumDeserializer;
  Object.defineProperty(product, installedMarker, { value: true });
}
