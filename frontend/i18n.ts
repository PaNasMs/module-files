import messages from "./server-messages.json";
import {
  registerTranslations,
  registerServerMessages,
  translator,
} from "@ostojaos/i18n";
import en from "./locales/en.json";
import ru from "./locales/ru.json";
import uk from "./locales/uk.json";
registerTranslations("files", { en, ru, uk });
registerServerMessages("files", messages);
export const tr = translator("files");
export { locale } from "@ostojaos/i18n";
