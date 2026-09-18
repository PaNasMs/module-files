import io
import os
import resource
import stat
import warnings


def render(path):
    resource.setrlimit(resource.RLIMIT_AS, (512 * 1024 * 1024, 512 * 1024 * 1024))
    resource.setrlimit(resource.RLIMIT_CPU, (10, 10))
    from PIL import Image, ImageOps

    Image.MAX_IMAGE_PIXELS = 40_000_000
    warnings.simplefilter("error", Image.DecompressionBombWarning)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 64 * 1024 * 1024:
            raise ValueError("Unsupported thumbnail source")
        with Image.open(source, formats=["JPEG", "PNG", "GIF", "WEBP", "BMP", "TIFF"]) as picture:
            picture.seek(0)
            picture.thumbnail((256, 256))
            picture = ImageOps.exif_transpose(picture)
            rgba = picture.convert("RGBA")
            background = Image.new("RGB", rgba.size, "white")
            background.paste(rgba, mask=rgba.getchannel("A"))
            result = io.BytesIO()
            background.save(result, format="JPEG", quality=82)
            return result.getvalue()
