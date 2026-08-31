import { addDays, isoDate } from "./period.svelte";

export const RETAIN_DAYS = 400;
export const HOURLY_DAYS = 90;
export const TODAY = isoDate(new Date());
export const RETAIN_FROM = isoDate(addDays(new Date(), -(RETAIN_DAYS - 1)));